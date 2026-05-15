package pipe

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
)

type data struct {
	ID int `json:"id" xml:"id" url:"id"`
}

func TestURL(t *testing.T) {
	tests := []struct {
		name     string
		base     string
		endpoint string
		params   any
		wantErr  bool
		want     string
	}{
		{
			name:    "must_error_on_url_parse_error",
			base:    "[::1",
			wantErr: true,
		},
		{
			name:    "must_error_on_go_querystring_error",
			base:    "/",
			params:  1,
			wantErr: true,
		},
		{
			name: "must_resolve_on_empty",
			want: "",
		},
		{
			name: "must_resolve_on_base_only",
			base: "/base/",
			want: "/base",
		},
		{
			name:     "must_resolve_on_endpoint_only",
			endpoint: "/endpoint/",
			want:     "/endpoint",
		},
		{
			name:     "must_resolve_on_base_and_endpoint",
			base:     "/base/",
			endpoint: "/endpoint/",
			want:     "/base/endpoint",
		},
		{
			name:     "must_resolve_on_base_endpoint_and_params",
			base:     "/base/",
			endpoint: "/endpoint/",
			params:   data{ID: 1},
			want:     "/base/endpoint?id=1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := URL(tt.base, tt.endpoint, tt.params)
			if gotErr := (err != nil); gotErr != tt.wantErr {
				t.Errorf("got error %v, want %v", gotErr, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got '%v', want '%v'", got, tt.want)
			}
		})
	}
}

func TestJSONCodec(t *testing.T) {
	codec := JSONCodec{}
	id := 1
	dat := data{ID: id}
	wantString := fmt.Sprintf(`{"id":%d}`, id)
	wantType := "application/json"

	gotBytes, err := codec.Marshal(dat)
	if err != nil {
		t.Errorf("failed codec.Marshal: %s", err.Error())
	}
	if string(gotBytes) != wantString {
		t.Errorf("got bytes '%v', want '%v'", string(gotBytes), wantString)
	}

	dat = data{}
	if err := codec.Unmarshal([]byte(wantString), &dat); err != nil {
		t.Errorf("failed codec.Umarshal: %s", err.Error())
	}
	if dat.ID != id {
		t.Errorf("got id '%v', want '%v'", dat.ID, id)
	}

	if got := codec.ContentType(); got != wantType {
		t.Errorf("got content-type '%v', want '%v'", got, wantType)
	}
}

func TestXMLCodec(t *testing.T) {
	codec := XMLCodec{}
	id := 1
	dat := data{ID: id}
	wantString := fmt.Sprintf("<data><id>%d</id></data>", id)
	wantType := "application/xml"

	gotBytes, err := codec.Marshal(dat)
	if err != nil {
		t.Errorf("failed codec.Marshal: %s", err.Error())
	}
	if string(gotBytes) != wantString {
		t.Errorf("got bytes '%v', want '%v'", string(gotBytes), wantString)
	}

	dat = data{}
	if err := codec.Unmarshal([]byte(wantString), &dat); err != nil {
		t.Errorf("failed codec.Umarshal: %s", err.Error())
	}
	if dat.ID != id {
		t.Errorf("got id '%v', want '%v'", dat.ID, id)
	}

	if got := codec.ContentType(); got != wantType {
		t.Errorf("got content-type '%v', want '%v'", got, wantType)
	}
}

func TestWithCodec(t *testing.T) {
	pipe := &Pipe{}
	codec := XMLCodec{}
	WithCodec(codec)(pipe)
	if pipe.codec != codec {
		t.Errorf("got '%#v', want '%#v'", pipe.codec, codec)
	}
}

func TestWithClient(t *testing.T) {
	pipe := &Pipe{}
	client := &http.Client{}
	WithClient(client)(pipe)
	if pipe.client != client {
		t.Errorf("got '%#v', want '%#v'", pipe.client, client)
	}
}

func TestNew(t *testing.T) {
	client := &http.Client{}

	tests := []struct {
		name       string
		codec      Codec
		client     *http.Client
		wantCodec  Codec
		wantClient *http.Client
	}{
		{
			name:       "must_use_defaults_on_no_options",
			wantCodec:  JSONCodec{},
			wantClient: http.DefaultClient,
		},
		{
			name:       "must_use_supplied_options",
			codec:      XMLCodec{},
			client:     client,
			wantCodec:  XMLCodec{},
			wantClient: client,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pipe *Pipe
			if tt.codec == nil && tt.client == nil {
				pipe = New()
			} else {
				pipe = New(WithCodec(tt.codec), WithClient(tt.client))
			}

			if pipe.codec != tt.wantCodec {
				t.Errorf("got coded '%#v', want '%#v'", pipe.codec, tt.wantCodec)
			}
			if pipe.client != tt.wantClient {
				t.Errorf("got client '%#v', want '%#v'", pipe.client, tt.wantClient)
			}
		})
	}
}

func TestPipe_NewRequestWithContext(t *testing.T) {
	tests := []struct {
		name     string
		method   string
		body     any
		wantErr  bool
		wantBody string
	}{
		{
			name:    "must_error_on_codec_marshal_error",
			method:  http.MethodGet,
			body:    make(chan int),
			wantErr: true,
		},
		{
			name:    "must_error_on_new_request_with_context_error",
			method:  "?",
			wantErr: true,
		},
		{
			name:     "must_resolve_with_body_nil",
			method:   http.MethodGet,
			wantBody: "",
		},
		{
			name:     "must_resolve_with_body",
			method:   http.MethodGet,
			body:     data{ID: 1},
			wantBody: `{"id":1}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipe := New()
			req, err := pipe.NewRequestWithContext(context.Background(), tt.method, "/", tt.body)
			if got := (err != nil); got != tt.wantErr {
				t.Errorf("got error %v, want %v", got, tt.wantErr)
			}
			if req != nil {
				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("failed to read body: %s", err.Error())
				}
				gotBody := string(body)
				if gotBody != tt.wantBody {
					t.Errorf("got body '%v', want '%v'", gotBody, tt.wantBody)
				}
				if gotBody != "" {
					wantType := pipe.codec.ContentType()
					if got := req.Header.Get("content-type"); got != wantType {
						t.Errorf("got content-type '%v', want '%v'", got, wantType)
					}
					if got := req.Header.Get("accept"); got != wantType {
						t.Errorf("got content-type '%v', want '%v'", got, wantType)
					}
				}
			}
		})
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }

type errReader struct{}

func (e errReader) Read(p []byte) (int, error) { return 0, fmt.Errorf("simulated read error") }
func (e errReader) Close() error               { return nil }

func TestPipe_Do(t *testing.T) {
	tests := []struct {
		name    string
		tripper http.RoundTripper
		wantErr bool
		wantOut data
	}{
		{
			name: "must_error_on_do_error",
			tripper: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return nil, fmt.Errorf("simulated network error")
			}),
			wantErr: true,
		},
		{
			name: "must_error_on_io_read_all_error",
			tripper: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       errReader{},
					Request:    r,
				}, nil
			}),
			wantErr: true,
		},
		{
			name: "must_error_on_response_gte_bad_request",
			tripper: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Request:    r,
				}, nil
			}),
			wantErr: true,
		},
		{
			name: "must_error_on_codec_unmarshal_error",
			tripper: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString("{")),
					Request:    r,
				}, nil
			}),
			wantErr: true,
		},
		{
			name: "must_resolve_ok",
			tripper: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Request:    r,
				}, nil
			}),
			wantOut: (data{}),
		},

		{
			name: "must_resolve_ok_and_unmarshal",
			tripper: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{"id":1}`)),
					Request:    r,
				}, nil
			}),
			wantOut: data{ID: 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pipe := New(WithClient(&http.Client{Transport: tt.tripper}))
			req, err := pipe.NewRequestWithContext(context.Background(), http.MethodGet, "/", nil)
			if err != nil {
				t.Fatalf("failed to create request: %s", err.Error())
			}

			var out data
			_, err = pipe.Do(req, &out)
			if got := (err != nil); got != tt.wantErr {
				t.Errorf("got error %v, want %v", got, tt.wantErr)
				t.Log(err.Error())
			}
			if out != tt.wantOut {
				t.Errorf("got out '%#v', want '%#v'", out, tt.wantOut)
			}
		})
	}
}
