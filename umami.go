package traefik_rybbit_feeder

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"net/netip"
	"os"
	"path"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Config the plugin configuration.
type Config struct {
	// Disabled disables the plugin.
	Disabled bool `json:"disabled"`
	// Debug enables debug logging, be prepared for flooding.
	Debug bool `json:"debug"`
	// QueueSize defines the size of queue, i.e. the amount of events that are waiting to be submitted to Rybbit.
	QueueSize int `json:"queueSize"`
	// BatchSize defines the amount of events that are submitted to Rybbit in one request, should always be 1.
	BatchSize int `json:"batchSize"`
	// BatchMaxWait defines the maximum time to wait before submitting the batch. Should be 1 second.
	BatchMaxWait time.Duration `json:"batchMaxWait"`

	// Host is the URL of the Rybbit instance.
	Host string `json:"host"`
	// APIKey is the API Key generated in Site Settings for a Rybbit Website
	APIKey string `json:"apiKey"`

	// Websites is a map of domain to site-id, which is required
	Websites map[string]string `json:"websites"`

	// TrackErrors defines whether errors (status codes >= 400) should be tracked.
	TrackErrors bool `json:"trackErrors"`
	// TrackAllResources defines whether all requests for any resource should be tracked.
	// By default, only requests that are believed to contain content are tracked.
	TrackAllResources bool `json:"trackAllResources"`
	// TrackExtensions defines an alternative list of file extensions that should be tracked.
	TrackExtensions []string `json:"trackExtensions"`

	// IgnoreUserAgents is a list of user agents to ignore.
	IgnoreUserAgents []string `json:"ignoreUserAgents"`
	// IgnoreURLs is a list of request urls to ignore, each string is converted to RegExp and urls matched against it.
	IgnoreURLs []string `json:"ignoreURLs"`
	// IgnoreIPs is a list of IPs or CIDRs to ignore.
	IgnoreIPs []string `json:"ignoreIPs"`
	// headerIp Header associated to real IP
	HeaderIp string `json:"headerIp"`
}

// CreateConfig creates the default plugin configuration.
func CreateConfig() *Config {
	return &Config{
		Disabled:     false,
		Debug:        false,
		QueueSize:    1000,
		BatchSize:    20,
		BatchMaxWait: 5 * time.Second,
		TrackErrors:  false,

		Host:   "",
		APIKey: "",

		Websites: map[string]string{},

		TrackAllResources: false,
		TrackExtensions:   []string{},

		IgnoreUserAgents: []string{},
		IgnoreURLs:       []string{},
		IgnoreIPs:        []string{},
		HeaderIp:         "X-Real-Ip",
	}
}

// UmamiFeeder a UmamiFeeder plugin.
type UmamiFeeder struct {
	next       http.Handler
	name       string
	isDebug    bool
	isDisabled bool
	logHandler *log.Logger
	queue      chan *RybbitEvent

	batchSize    int
	batchMaxWait time.Duration

	host              string
	apiKey            string
	websites          map[string]string
	websitesMutex     sync.RWMutex
	createNewWebsites bool

	trackErrors       bool
	trackAllResources bool
	trackExtensions   []string

	ignoreUserAgents []string
	ignoreRegexps    []regexp.Regexp
	ignorePrefixes   []netip.Prefix
	headerIp         string
}

// New created a new Demo plugin.
func New(ctx context.Context, next http.Handler, config *Config, name string) (http.Handler, error) {
	// construct
	h := &UmamiFeeder{
		next:       next,
		name:       name,
		isDebug:    config.Debug,
		isDisabled: config.Disabled,
		logHandler: log.New(os.Stdout, "", 0),

		queue:        make(chan *RybbitEvent, config.QueueSize),
		batchSize:    config.BatchSize,
		batchMaxWait: 1 * time.Second,

		host:          config.Host,
		apiKey:        config.APIKey,
		websites:      config.Websites,
		websitesMutex: sync.RWMutex{},

		trackErrors:       config.TrackErrors,
		trackAllResources: config.TrackAllResources,
		trackExtensions:   config.TrackExtensions,

		ignoreUserAgents: config.IgnoreUserAgents,
		ignoreRegexps:    []regexp.Regexp{},
		ignorePrefixes:   []netip.Prefix{},
		headerIp:         config.HeaderIp,
	}

	if !h.isDisabled {
		h.isDisabled = true
		h.debug("batchSize %d", h.batchSize)
		h.debug("batchMaxWait %v", h.batchMaxWait)
		go h.retryConnection(ctx, config)
	}

	return h, nil
}

func (h *UmamiFeeder) retryConnection(ctx context.Context, config *Config) {
	const maxRetryInterval = time.Hour
	retryAttempt := 0
	for {
		currentDelay := maxRetryInterval
		if retryAttempt == 0 {
			currentDelay = 0
		} else if retryAttempt < 8 {
			currentDelay = time.Duration(15*math.Pow(2, float64(retryAttempt))) * time.Second
		}

		if retryAttempt > 0 { // Don't log for the immediate first attempt
			h.debug("Next connection attempt in %v (attempt #%d).", currentDelay, retryAttempt+1)
		}

		select {
		case <-time.After(currentDelay):
			retryAttempt++
			h.debug("Attempting to connect to Rybbit (attempt #%d)...", retryAttempt)

			err := h.connect(ctx, config)
			if err == nil {
				h.debug("Successfully connected to Rybbit. Verifying configuration...")

				err = h.verifyConfig(config)
				if err == nil {
					h.debug("Configuration verified. Enabling plugin and starting worker.")
					h.isDisabled = false
					go h.startWorker(ctx)
					return // Successfully connected and configured, exit retry goroutine
				}

				h.error("configuration error, the plugin is disabled: " + err.Error())
				h.isDisabled = true
				return // Exit retry goroutine, plugin remains disabled.
			}

			h.error("Failed to reconnect to Rybbit: " + err.Error())
		case <-ctx.Done():
			h.debug("Context cancelled during retryConnection, stopping connection retries.")
			return
		}
	}
}

func (h *UmamiFeeder) connect(ctx context.Context, config *Config) error {
	if h.host == "" {
		return fmt.Errorf("`host` is not set")
	}

	if h.apiKey == "" {
		return fmt.Errorf("`apiKey` should be set")
	}

	if len(h.websites) == 0 {
		return fmt.Errorf("`websites` should not be empty")
	}

	_, err := sendRequest(ctx, h.host+"/health", nil, nil)
	if err != nil {
		return fmt.Errorf("Failed to get health for rybbit: %w", err)
	}

	return nil
}

func (h *UmamiFeeder) verifyConfig(config *Config) error {
	if len(config.IgnoreIPs) > 0 {
		for _, ignoreIp := range config.IgnoreIPs {
			network, err := netip.ParsePrefix(ignoreIp)
			if err != nil {
				network, err = netip.ParsePrefix(ignoreIp + "/32")
			}

			if err != nil || !network.IsValid() {
				return fmt.Errorf("invalid ignoreIp given %s: %w", ignoreIp, err)
			}

			h.ignorePrefixes = append(h.ignorePrefixes, network)
		}
	}

	if len(config.IgnoreURLs) > 0 {
		for _, location := range config.IgnoreURLs {
			r, err := regexp.Compile(location)
			if err != nil {
				return fmt.Errorf("failed to compile ignoreURL %s: %w", location, err)
			}

			h.ignoreRegexps = append(h.ignoreRegexps, *r)
		}
	}

	return nil
}

func (h *UmamiFeeder) ServeHTTP(rw http.ResponseWriter, req *http.Request) {
	if !h.isDisabled && h.shouldTrack(req) {
		// If the resource should be reported, we wrap the response writer and check the status code before reporting
		wrappedResponseWriter := &ResponseWriter{
			ResponseWriter: rw,
			request:        req,
			feeder:         h,
		}

		// Continue with next handler.
		h.next.ServeHTTP(wrappedResponseWriter, req)
		return
	}

	h.next.ServeHTTP(rw, req)
}

func (h *UmamiFeeder) shouldTrack(req *http.Request) bool {
	if len(h.ignorePrefixes) > 0 {
		requestIp := req.Header.Get(h.headerIp)
		if requestIp == "" {
			requestIp = req.RemoteAddr
		}

		ip, err := netip.ParseAddr(requestIp)
		if err != nil {
			h.debug("invalid IP %s", requestIp)
			return false
		}

		for _, prefix := range h.ignorePrefixes {
			if prefix.Contains(ip) {
				h.debug("ignoring IP %s", ip)
				return false
			}
		}
	}

	if len(h.ignoreUserAgents) > 0 {
		userAgent := req.UserAgent()
		for _, disabledUserAgent := range h.ignoreUserAgents {
			if strings.Contains(userAgent, disabledUserAgent) {
				h.debug("ignoring user-agent %s", userAgent)
				return false
			}
		}
	}

	if len(h.ignoreRegexps) > 0 {
		requestURL := req.URL.String()
		for _, r := range h.ignoreRegexps {
			if r.MatchString(requestURL) {
				h.debug("ignoring location %s", requestURL)
				return false
			}
		}
	}

	if !h.shouldTrackResource(req.URL.Path) {
		h.debug("ignoring resource %s", req.URL.Path)
		return false
	}

	if h.createNewWebsites {
		return true
	}

	hostname := parseDomainFromHost(req.Host)
	if _, ok := h.websites[hostname]; ok {
		return true
	}

	h.debug("ignoring domain %s", hostname)
	return false
}

func (h *UmamiFeeder) shouldTrackResource(url string) bool {
	if h.trackAllResources {
		return true
	}

	pathExt := path.Ext(url)

	// If a custom file extension list is defined, check if the resource matches it. If not, do not report.
	if len(h.trackExtensions) > 0 {
		for _, suffix := range h.trackExtensions {
			if suffix == pathExt {
				return true
			}
		}
		return false
	}

	// Check if the suffix is regarded to be "content".
	switch pathExt {
	case "", ".htm", ".html", ".xhtml", ".jsf", ".md", ".php", ".rss", ".rtf", ".txt", ".xml", ".pdf":
		return true
	}

	return false
}

func (h *UmamiFeeder) shouldTrackStatus(statusCode int) (report bool) {
	if statusCode >= 400 {
		if h.trackErrors {
			return true
		}

		h.debug("not reporting %d error", statusCode)
		return false
	}
	return true
}

func (h *UmamiFeeder) error(message string) {
	if h.logHandler != nil {
		now := time.Now().Format("2006-01-02T15:04:05Z")
		h.logHandler.Printf("%s ERR middlewareName=%s error=\"%s\"", now, h.name, message)
	}
}

// Arguments are handled in the manner of [fmt.Printf].
func (h *UmamiFeeder) debug(format string, v ...any) {
	if h.logHandler != nil && h.isDebug {
		now := time.Now().Format("2006-01-02T15:04:05Z")
		h.logHandler.Printf("%s DBG middlewareName=%s msg=\"%s\"", now, h.name, fmt.Sprintf(format, v...))
	}
}
