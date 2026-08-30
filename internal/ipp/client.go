// Package ipp speaks IPP to a CUPS server.
//
// Every interaction printer-cycle has with a printer goes through here. Two
// things this package deliberately never does:
//
// It never shells out to lp, lpstat, or lpadmin. Those tools are themselves thin
// IPP clients, so invoking one would fork a process in order to send a request
// this package is already connected to send, and it would add a runtime
// dependency on the CUPS command line tools for no gain.
//
// It never parses human-readable CUPS output. The format of lpstat varies by
// CUPS version and by locale, so building on it would produce software that
// breaks when someone's machine has a different LANG set.
package ipp

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sync/atomic"

	"github.com/OpenPrinting/goipp"
)

// contentType is the MIME type every IPP message travels as, per RFC 8010.
const contentType = "application/ipp"

// Client sends IPP requests to a CUPS server.
//
// The zero value is not usable. Call [New].
type Client struct {
	httpc *http.Client

	// endpoint is where requests are POSTed. When talking over a Unix socket the
	// host is a placeholder, because the dialer discards the address it is given.
	endpoint *url.URL

	// authority is the host used when building the ipp:// URIs that CUPS expects
	// in printer-uri and job-uri attributes.
	authority string

	// nextID hands out IPP request identifiers. They only need to be unique
	// within a connection, so a counter is enough.
	nextID atomic.Uint32
}

// New builds a client for a CUPS endpoint. Two forms are accepted:
//
//	unix:///run/cups/cups.sock   production, cupsd's own socket
//	http://127.0.0.1:6631        development, or a CUPS on another machine
//
// IPP is identical over either transport, so nothing above this layer has to
// know which is in use. That is the whole reason the development environment can
// run CUPS in a container over TCP while production uses the socket.
func New(endpoint string) (*Client, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("ipp: parsing endpoint %q: %w", endpoint, err)
	}

	switch u.Scheme {
	case "unix":
		if u.Path == "" {
			return nil, fmt.Errorf("ipp: endpoint %q has no socket path", endpoint)
		}
		socket := u.Path
		c := &Client{
			httpc: &http.Client{
				Transport: &http.Transport{
					DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						var d net.Dialer
						return d.DialContext(ctx, "unix", socket)
					},
				},
			},
			// The host is arbitrary since the dialer ignores it, but it has to be
			// present and valid. "localhost" keeps generated URIs readable.
			endpoint:  &url.URL{Scheme: "http", Host: "localhost"},
			authority: "localhost",
		}
		return c, nil

	case "http", "https":
		if u.Host == "" {
			return nil, fmt.Errorf("ipp: endpoint %q has no host", endpoint)
		}
		c := &Client{
			httpc:     &http.Client{Transport: &http.Transport{}},
			endpoint:  &url.URL{Scheme: u.Scheme, Host: u.Host},
			authority: u.Host,
		}
		return c, nil

	default:
		return nil, fmt.Errorf("ipp: endpoint %q: scheme must be unix, http, or https", endpoint)
	}
}

// NewRequest starts a request for op, with the two attributes RFC 8011 requires
// to come first in every operation group. Callers add the rest.
func (c *Client) NewRequest(op goipp.Op) *goipp.Message {
	m := goipp.NewRequest(goipp.DefaultVersion, op, c.nextID.Add(1))
	m.Operation.Add(goipp.MakeAttribute("attributes-charset", goipp.TagCharset, goipp.String("utf-8")))
	m.Operation.Add(goipp.MakeAttribute("attributes-natural-language", goipp.TagLanguage, goipp.String("en-us")))
	return m
}

// PrinterURI builds the URI CUPS expects in a printer-uri attribute.
func (c *Client) PrinterURI(name string) string {
	return "ipp://" + c.authority + "/printers/" + url.PathEscape(name)
}

// RootURI addresses the server rather than any one printer. Server-wide
// operations such as CUPS-Get-Devices want this.
func (c *Client) RootURI() string {
	return "ipp://" + c.authority + "/"
}

// Do sends one IPP request and returns the response message.
//
// path is the HTTP resource to POST to: "/" for server-wide operations,
// "/printers/<name>" for a single printer.
//
// body, when not nil, is streamed immediately after the encoded message. That is
// how document data travels in Print-Job. Because the combined length is not
// known in advance, Go sends the request chunked, which means a large document
// is never held in memory. On a 512MB Raspberry Pi that is not an optimisation,
// it is the difference between working and being killed by the kernel.
//
// A non-nil error means the exchange itself failed. It does not mean the
// operation failed: IPP reports that as a status code on the returned message,
// which the caller is responsible for checking.
func (c *Client) Do(ctx context.Context, path string, req *goipp.Message, body io.Reader) (*goipp.Message, error) {
	resp, err := c.send(ctx, path, req, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	msg := &goipp.Message{}
	if err := msg.Decode(resp.Body); err != nil {
		return nil, fmt.Errorf("ipp: decoding response: %w", err)
	}
	return msg, nil
}

// send performs the exchange and hands back the undecoded response body.
//
// Do covers almost every caller. This exists for the one that cannot wait for a
// complete message: CUPS streams CUPS-Get-Devices, sending each device as a
// backend finds it, and decoding only at the end would throw away the entire
// point of that. The caller owns closing the body.
func (c *Client) send(ctx context.Context, path string, req *goipp.Message, body io.Reader) (*http.Response, error) {
	var head bytes.Buffer
	if err := req.Encode(&head); err != nil {
		return nil, fmt.Errorf("ipp: encoding request: %w", err)
	}

	var payload io.Reader = &head
	if body != nil {
		payload = io.MultiReader(&head, body)
	}

	u := *c.endpoint
	u.Path = path

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), payload)
	if err != nil {
		return nil, fmt.Errorf("ipp: building request: %w", err)
	}
	httpReq.Header.Set("Content-Type", contentType)

	resp, err := c.httpc.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("ipp: sending request: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("ipp: server returned HTTP %s", resp.Status)
	}
	return resp, nil
}
