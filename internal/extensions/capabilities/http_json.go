package capabilities

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

type httpJSONClient struct {
	client *http.Client
}

func newHTTPClient(proxyURL string) (*http.Client, error) {
	transport := &http.Transport{Proxy: http.ProxyFromEnvironment}
	if strings.TrimSpace(proxyURL) != "" {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, err
		}
		switch parsed.Scheme {
		case "http", "https":
			transport.Proxy = http.ProxyURL(parsed)
		case "socks5", "socks5h":
			parsed.Scheme = "socks5"
			dialer, err := proxy.FromURL(parsed, proxy.Direct)
			if err != nil {
				return nil, err
			}
			transport.Proxy = nil
			transport.DialContext = func(ctx context.Context, network string, address string) (netConn net.Conn, err error) {
				type contextDialer interface {
					DialContext(context.Context, string, string) (net.Conn, error)
				}
				if d, ok := dialer.(contextDialer); ok {
					return d.DialContext(ctx, network, address)
				}
				return dialer.Dial(network, address)
			}
		default:
			return nil, fmt.Errorf("unsupported proxy scheme: %s", parsed.Scheme)
		}
	}
	return &http.Client{Timeout: 2 * time.Minute, Transport: transport}, nil
}

func newHTTPJSONClient(client *http.Client) httpJSONClient {
	return httpJSONClient{client: client}
}

func (c httpJSONClient) get(ctx context.Context, targetURL string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return err
	}
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	return c.do(req, out)
}

func (c httpJSONClient) post(ctx context.Context, targetURL string, headers map[string]string, body any, out any) error {
	data, err := json.Marshal(body)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, bytes.NewReader(data))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	return c.do(req, out)
}

func (c httpJSONClient) do(req *http.Request, out any) error {
	req.Header.Set("Accept", "application/json")
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s status %d: %s", req.URL.Host, resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return json.Unmarshal(data, out)
}
