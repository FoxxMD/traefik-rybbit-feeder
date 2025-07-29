package traefik_rybbit_feeder

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

type RybbitEvent struct {
	APIKey     string `json:"api_key"`
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
}

func (h *UmamiFeeder) submitToFeed(req *http.Request, code int) {
	hostname := parseDomainFromHost(req.Host)
	websiteId, ok := h.websites[hostname]

	if !ok {
		h.error("tracking skipped, site-id is unknown: " + hostname)
		return
	}

	rEvent := &RybbitEvent{
		APIKey:    h.apiKey,
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
			batch = append(batch, &SendBody{Payload: event, Type: "event"})
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
	for _, value := range events {
		resp, err := sendRequest(ctx, h.host+"/api/track", value.Payload, nil)
		if err != nil {
			h.error("failed to send tracking: " + err.Error())
			return
		}
		if h.isDebug {
			bodyBytes, _ := io.ReadAll(resp.Body)
			h.debug("%v: %s", resp.Status, string(bodyBytes))
		}
		defer func() {
			_ = resp.Body.Close()
		}()
	}
}
