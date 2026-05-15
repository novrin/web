package pipe

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"

	"github.com/google/go-querystring/query"
)

// URL returns base, endpoint, and params encoded into a single url string with
// query paramters.
//
// It uses net/url to parse base; expect mirrored error conditions.
// It uses path for endpoint normalization; expect the mirrored effects.
// It uses go-querystring for parameter values; expect mirrored error conditions.
func URL(base, endpoint string, params any) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}

	u.Path = path.Join(u.Path, endpoint)

	if params != nil {
		q, err := query.Values(params)
		if err != nil {
			return "", err
		}
		u.RawQuery = q.Encode()
	}

	return u.String(), nil
}

// Codec is the interface implemented by any object that encodes and decodes
// to and from a specific content type.
type Codec interface {
	Marshal(any) ([]byte, error)
	Unmarshal([]byte, any) error
	ContentType() string
}

// JSONCodec implements a JSON codec.
type JSONCodec struct{}

func (JSONCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (JSONCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
func (JSONCodec) ContentType() string                { return "application/json" }

// XMLCodec implements an XML codec.
type XMLCodec struct{}

func (XMLCodec) Marshal(v any) ([]byte, error)      { return xml.Marshal(v) }
func (XMLCodec) Unmarshal(data []byte, v any) error { return xml.Unmarshal(data, v) }
func (XMLCodec) ContentType() string                { return "application/xml" }

// Pipe funnels HTTP requests using an http.Client and a Codec.
type Pipe struct {
	codec  Codec
	client *http.Client
}

// Option is a function that sets a Pipe option.
type Option func(*Pipe)

// WithCodec sets Pipe to encode/decode the request/response body using codec.
func WithCodec(codec Codec) Option { return func(p *Pipe) { p.codec = codec } }

// WithClient sets Pipe to funnel through client.
func WithClient(client *http.Client) Option { return func(p *Pipe) { p.client = client } }

// New returns a new Pipe configured with the given options.
// If no options are supplied, JSONCodec and http.DefaultClient are used.
func New(opts ...Option) *Pipe {
	pipe := &Pipe{codec: JSONCodec{}, client: http.DefaultClient}

	for _, opt := range opts {
		opt(pipe)
	}

	return pipe
}

// NewRequestWithContext returns a new request after successfuly encoding body
// with codec and setting the content-type and accept header fields.
func (pipe *Pipe) NewRequestWithContext(ctx context.Context, method, url string, body any) (*http.Request, error) {
	var buf []byte
	var err error
	if body != nil {
		buf, err = pipe.codec.Marshal(body)
		if err != nil {
			return nil, err
		}
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}

	if body != nil {
		req.Header.Set("Content-Type", pipe.codec.ContentType())
		req.Header.Set("Accept", pipe.codec.ContentType())
	}

	return req, nil
}

// Do executes the given request with context. If the response status code
// indicates an error (>= 400), then an error is returned. Otherwise, the
// body is consumed and decoded into out if supplied.
func (pipe *Pipe) Do(req *http.Request, out any) (*http.Response, error) {
	resp, err := pipe.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s -> %s: %s", resp.Request.Method, resp.Request.URL.String(), http.StatusText(resp.StatusCode), string(body))
	}

	if len(body) > 0 && out != nil {
		err = pipe.codec.Unmarshal(body, out)
		if err != nil {
			return nil, err
		}
	}

	return resp, nil
}
