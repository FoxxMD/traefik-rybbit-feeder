package traefik_rybbit_feeder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type RybbitEvent struct {
	SiteID     string `json:"site_id"`
	Type       string `json:"type"`
	Pathname   string `json:"pathname"`
	Hostname   string `json:"hostname,omitempty"`
	IP         string `json:"ip_address,omitempty"`
	UserAgent  string `json:"user_agent,omitempty"`
	Language   string `json:"language,omitempty"`
	EventName  string `json:"event_name,omitempty"`
	Referrer   string `json:"referrer,omitempty"`
	Properties string `json:"properties,omitempty"`
}

type SendBody struct {
	Payload *RybbitEvent `json:"payload"`
	Type    string       `json:"type"`
	ApiKey  string
}

func (h *UmamiFeeder) submitToFeed(req *http.Request, code int) {
	hostname := parseDomainFromHost(req.Host)
	websiteId, ok := h.websites[hostname]

	if !ok {
		h.error("tracking skipped, site-id is unknown: " + hostname)
		return
	}

	rEvent := &RybbitEvent{
		SiteID:    websiteId,
		Type:      "pageview",
		Pathname:  req.URL.Path,
		Hostname:  hostname,
		IP:        extractRemoteIP(req),
		UserAgent: req.Header.Get("User-Agent"),
		Referrer:  req.Referer(),
		Language:  parseAcceptLanguage(req.Header.Get("Accept-Language")),
	}

	select {
	case h.queue <- rEvent:
	default:
		h.error("failed to submit event: queue full")
	}
}

func (h *UmamiFeeder) startWorker(ctx context.Context) {
	for {
		err := h.umamiEventFeeder(ctx)
		if err != nil {
			h.error("worker failed: " + err.Error())
		} else {
			return
		}
	}
}

func (h *UmamiFeeder) umamiEventFeeder(ctx context.Context) (err error) {
	defer func() {
		// Recover from panic.
		panicVal := recover()
		if panicVal != nil {
			h.error("panic: " + fmt.Sprint(panicVal))
		}
	}()

	batch := make([]*SendBody, 0, h.batchSize)
	timeout := time.NewTimer(h.batchMaxWait)

	for {
		// Wait for event.
		select {
		case <-ctx.Done():
			h.debug("worker shutting down (canceled)")
			if len(batch) > 0 {
				h.reportEventsToUmami(ctx, batch)
			}
			return nil

		case event := <-h.queue:
			batch = append(batch, &SendBody{Payload: event, Type: "event", ApiKey: h.apiKey})
			if len(batch) >= h.batchSize {
				h.reportEventsToUmami(ctx, batch)
				batch = make([]*SendBody, 0, h.batchSize)
				timeout.Reset(h.batchMaxWait)
			}

		case <-timeout.C:
			if len(batch) > 0 {
				h.reportEventsToUmami(ctx, batch)
				batch = make([]*SendBody, 0, h.batchSize)
			}
			timeout.Reset(h.batchMaxWait)
		}
	}
}

func (h *UmamiFeeder) reportEventsToUmami(ctx context.Context, events []*SendBody) {
	h.debug("reporting %d events", len(events))
	trackURL := h.host + "/api/track"
	for _, value := range events {
		// A failed event is logged and skipped so it doesn't drop the rest of the batch.
		if err := h.sendEvent(ctx, trackURL, value); err != nil {
			h.error("failed to send tracking: " + err.Error())
		}
	}
}

func (h *UmamiFeeder) sendEvent(ctx context.Context, trackURL string, value *SendBody) error {
	if h.isDebug {
		summary, err := json.Marshal(struct {
			URL     string       `json:"url"`
			Payload *RybbitEvent `json:"payload"`
			ApiKey  string       `json:"apiKey"`
		}{trackURL, value.Payload, redactKey(value.ApiKey)})
		if err != nil {
			h.debug("could not encode event summary: %v", err)
		} else {
			h.debug("sending event %s", summary)
		}
	}
	headers := map[string][]string{
		"Authorization": {"Bearer " + value.ApiKey},
	}
	resp, err := sendRequest(ctx, trackURL, value.Payload, headers)
	if err != nil {
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if h.isDebug {
		bodyBytes, _ := io.ReadAll(resp.Body)
		h.debug("%v: %s", resp.Status, string(bodyBytes))
	}
	return nil
}

// redactKey keeps just enough of an API key to tell keys apart in logs.
func redactKey(key string) string {
	if len(key) < 12 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}
