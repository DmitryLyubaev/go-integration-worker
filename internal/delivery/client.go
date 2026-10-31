package delivery

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/DmitryLyubaev/go-integration-worker/internal/domain"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	base string
	http *http.Client
}

func NewClient(base string) (*Client, error) {
	u, e := url.Parse(base)
	if e != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("receiver must be an owned loopback HTTP endpoint")
	}
	ip := net.ParseIP(u.Hostname())
	if u.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback()) {
		return nil, errors.New("receiver must be loopback")
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 500 * time.Millisecond, KeepAlive: 30 * time.Second}).DialContext, MaxIdleConns: 2, MaxIdleConnsPerHost: 2, MaxConnsPerHost: 2, DisableCompression: true}
	return &Client{strings.TrimRight(base, "/"), &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}
func (c *Client) Send(ctx context.Context, p domain.Payload) domain.Outcome {
	request, e := http.NewRequestWithContext(ctx, "POST", c.base+"/exports", bytes.NewReader(p.Bytes))
	if e != nil {
		return domain.Outcome{Kind: domain.Unrecognized, Ambiguous: true, Reason: "invalid request"}
	}
	request.GetBody = nil
	request.Header.Set("Content-Type", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(request.Header))
	response, e := c.http.Do(request)
	if e != nil {
		kind := domain.Retryable
		if errors.Is(ctx.Err(), context.Canceled) {
			kind = domain.Canceled
		}
		return domain.Outcome{Kind: kind, Ambiguous: true, Reason: "HTTP transport failure"}
	}
	defer response.Body.Close()
	reason := fmt.Sprintf("HTTP %d", response.StatusCode)
	switch response.StatusCode {
	case 200, 201:
		raw, readError := io.ReadAll(io.LimitReader(response.Body, 4097))
		if readError != nil || len(raw) > 4096 {
			return domain.Outcome{Kind: domain.Retryable, Ambiguous: true, Reason: "incomplete or oversized success response"}
		}
		var receipt domain.Receipt
		if e = json.Unmarshal(raw, &receipt); e != nil || receipt.EventID != p.Event.EventID || receipt.PayloadHash != p.Hash || receipt.ReceiptID == "" {
			return domain.Outcome{Kind: domain.Retryable, Ambiguous: true, Reason: "mismatched success receipt"}
		}
		return domain.Outcome{Kind: domain.Success, Receipt: &receipt, Reason: reason}
	case 400, 409, 422:
		return domain.Outcome{Kind: domain.Permanent, Reason: reason}
	case 408, 429, 500, 502, 503, 504:
		out := domain.Outcome{Kind: domain.Retryable, Reason: reason}
		if response.StatusCode == 429 {
			out.RetryAfter = retryAfter(response.Header.Get("Retry-After"), time.Now())
			if out.RetryAfter > time.Second {
				out.Reason += "; Retry-After exceeds demonstration limit"
			}
		}
		return out
	default:
		return domain.Outcome{Kind: domain.Unrecognized, Ambiguous: true, Reason: reason}
	}
}
func retryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	digits := value != ""
	for _, character := range value {
		if character < '0' || character > '9' {
			digits = false
			break
		}
	}
	if digits {
		normalized := strings.TrimLeft(value, "0")
		if normalized == "" {
			return 0
		}
		if normalized == "1" {
			return time.Second
		}
		return 2 * time.Second
	}
	if date, e := http.ParseTime(value); e == nil {
		duration := date.Sub(now)
		if duration > time.Second {
			return 2 * time.Second
		}
		if duration > 0 {
			return duration
		}
	}
	return 0
}
func (c *Client) Receipt(ctx context.Context, id string) (domain.Receipt, error) {
	var receipt domain.Receipt
	request, e := http.NewRequestWithContext(ctx, "GET", c.base+"/receipts/"+url.PathEscape(id), nil)
	if e != nil {
		return receipt, errors.New("invalid receipt request")
	}
	response, e := c.http.Do(request)
	if e != nil {
		return receipt, errors.New("receipt unavailable")
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 4097))
	if response.StatusCode != 200 || e != nil || len(raw) > 4096 {
		return receipt, errors.New("receipt missing or unavailable")
	}
	if e = json.Unmarshal(raw, &receipt); e != nil || receipt.EventID != id || receipt.PayloadHash == "" || receipt.ReceiptID == "" {
		return receipt, errors.New("receipt mismatch")
	}
	return receipt, nil
}
