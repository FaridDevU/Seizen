package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	editorGatewayPollInterval = 100 * time.Millisecond
	editorGatewayHTMLLimit    = 4 << 20
)

const editorWorkbenchConfigurationMarker = `<meta id="vscode-workbench-web-configuration" data-settings="`

var errEditorGatewayRedirect = errors.New("VS Code tried to leave its private address")

type editorGatewayStarter func(context.Context, string, string, string) (string, func() error, error)

func startEditorGateway(ctx context.Context, upstreamOrigin, basePath, connectionToken string) (string, func() error, error) {
	upstream, err := url.Parse(upstreamOrigin)
	if err != nil || upstream.Scheme != "http" || upstream.Hostname() != "127.0.0.1" || upstream.Port() == "" || upstream.Path != "" || upstream.RawQuery != "" || upstream.Fragment != "" {
		return "", nil, errors.New("VS Code announced an invalid local address")
	}
	if !validEditorBasePath(basePath) || connectionToken == "" {
		return "", nil, errors.New("the private VS Code session is not valid")
	}

	jar, err := bootstrapEditorGateway(ctx, http.DefaultTransport, upstream, basePath, connectionToken)
	if err != nil {
		return "", nil, fmt.Errorf("VS Code did not accept the private session: %w", err)
	}

	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return "", nil, fmt.Errorf("could not open the local VS Code panel: %w", err)
	}
	if err = ctx.Err(); err != nil {
		_ = listener.Close()
		return "", nil, err
	}
	gatewayOrigin := "http://" + listener.Addr().String()
	handler := newEditorGatewayHandler(listener.Addr().String(), gatewayOrigin, upstream, basePath, connectionToken, jar, http.DefaultTransport)

	var connectionsMu sync.Mutex
	connections := make(map[net.Conn]struct{})
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(io.Discard, "", 0),
		ConnState: func(connection net.Conn, state http.ConnState) {
			connectionsMu.Lock()
			defer connectionsMu.Unlock()
			switch state {
			case http.StateNew:
				connections[connection] = struct{}{}
			case http.StateClosed:
				delete(connections, connection)
			}
		},
	}
	go func() { _ = server.Serve(listener) }()

	var closeOnce sync.Once
	var closeErr error
	closeGateway := func() error {
		closeOnce.Do(func() {
			closeErr = server.Close()
			connectionsMu.Lock()
			openConnections := make([]net.Conn, 0, len(connections))
			for connection := range connections {
				openConnections = append(openConnections, connection)
				delete(connections, connection)
			}
			connectionsMu.Unlock()
			for _, connection := range openConnections {
				_ = connection.Close()
			}
			if errors.Is(closeErr, http.ErrServerClosed) {
				closeErr = nil
			}
		})
		return closeErr
	}
	return gatewayOrigin + basePath + "/", closeGateway, nil
}

func bootstrapEditorGateway(ctx context.Context, transport http.RoundTripper, upstream *url.URL, basePath, connectionToken string) (*cookiejar.Jar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{
		Transport: transport,
		Jar:       jar,
		CheckRedirect: func(request *http.Request, _ []*http.Request) error {
			if request.URL.Scheme != upstream.Scheme || request.URL.Host != upstream.Host || !editorGatewayPathAllowed(request.URL.Path, basePath) {
				return errEditorGatewayRedirect
			}
			return nil
		},
	}
	bootstrapURL := *upstream
	bootstrapURL.Path = basePath + "/"
	query := bootstrapURL.Query()
	query.Set("tkn", connectionToken)
	bootstrapURL.RawQuery = query.Encode()
	cookieURL := bootstrapURL
	cookieURL.RawQuery = ""

	for {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, bootstrapURL.String(), nil)
		if requestErr != nil {
			return nil, requestErr
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				if len(jar.Cookies(&cookieURL)) > 0 {
					return jar, nil
				}
			}
		} else if errors.Is(requestErr, errEditorGatewayRedirect) {
			return nil, errEditorGatewayRedirect
		}

		timer := time.NewTimer(editorGatewayPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func newEditorGatewayHandler(expectedHost, gatewayOrigin string, upstream *url.URL, basePath, connectionToken string, jar *cookiejar.Jar, transport http.RoundTripper) http.Handler {
	proxy := &httputil.ReverseProxy{
		Transport: transport,
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(upstream)
			request.SetXForwarded()
			request.Out.Host = upstream.Host
			request.Out.Header.Del("Cookie")
			for _, cookie := range jar.Cookies(request.Out.URL) {
				request.Out.AddCookie(&http.Cookie{Name: cookie.Name, Value: cookie.Value})
			}
			if request.Out.Header.Get("Origin") != "" {
				request.Out.Header.Set("Origin", upstream.Scheme+"://"+upstream.Host)
			}
			query := request.Out.URL.Query()
			query.Del("tkn")
			request.Out.URL.RawQuery = query.Encode()
			if request.Out.URL.Path == basePath || request.Out.URL.Path == basePath+"/" {
				request.Out.Header.Del("Accept-Encoding")
			}
		},
		ModifyResponse: func(response *http.Response) error {
			if response.Request != nil && response.Request.URL != nil {
				jar.SetCookies(response.Request.URL, response.Cookies())
			}
			response.Header.Del("Set-Cookie")
			if err := injectEditorConnectionToken(response, basePath, connectionToken); err != nil {
				return err
			}
			location := response.Header.Get("Location")
			if location == "" {
				return nil
			}
			parsed, err := url.Parse(location)
			if err != nil {
				response.Header.Del("Location")
				return errEditorGatewayRedirect
			}
			base := upstream
			if response.Request != nil && response.Request.URL != nil {
				base = response.Request.URL
			}
			resolved := base.ResolveReference(parsed)
			if resolved.Scheme != upstream.Scheme || resolved.Host != upstream.Host || !editorGatewayPathAllowed(resolved.Path, basePath) {
				response.Header.Del("Location")
				return errEditorGatewayRedirect
			}
			query := resolved.Query()
			query.Del("tkn")
			resolved.RawQuery = query.Encode()
			gateway, _ := url.Parse(gatewayOrigin)
			resolved.Scheme, resolved.Host = gateway.Scheme, gateway.Host
			response.Header.Set("Location", resolved.String())
			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, _ *http.Request, _ error) {
			http.Error(writer, "VS Code is not available", http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Host != expectedHost || !editorGatewayPathAllowed(request.URL.Path, basePath) {
			http.NotFound(writer, request)
			return
		}
		proxy.ServeHTTP(writer, request)
	})
}

func injectEditorConnectionToken(response *http.Response, basePath, connectionToken string) error {
	if response.StatusCode != http.StatusOK || response.Request == nil || response.Request.URL == nil ||
		(response.Request.URL.Path != basePath && response.Request.URL.Path != basePath+"/") {
		return nil
	}
	if response.Header.Get("Content-Encoding") != "" || !strings.HasPrefix(strings.ToLower(response.Header.Get("Content-Type")), "text/html") {
		return errors.New("VS Code returned an invalid start page")
	}
	document, err := io.ReadAll(io.LimitReader(response.Body, editorGatewayHTMLLimit+1))
	closeErr := response.Body.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if len(document) > editorGatewayHTMLLimit {
		return errors.New("the VS Code page exceeds the allowed limit")
	}
	document, err = addEditorConnectionToken(document, connectionToken)
	if err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(document))
	response.ContentLength = int64(len(document))
	response.Header.Set("Content-Length", fmt.Sprintf("%d", len(document)))
	response.Header.Set("Cache-Control", "no-store")
	response.Header.Del("ETag")
	return nil
}

func addEditorConnectionToken(document []byte, connectionToken string) ([]byte, error) {
	marker := []byte(editorWorkbenchConfigurationMarker)
	start := bytes.Index(document, marker)
	if start < 0 {
		return nil, errors.New("VS Code did not include its web configuration")
	}
	valueStart := start + len(marker)
	valueEnd := bytes.IndexByte(document[valueStart:], '"')
	if valueEnd < 0 {
		return nil, errors.New("the VS Code web configuration is not valid")
	}
	valueEnd += valueStart
	configuration := make(map[string]any)
	if err := json.Unmarshal([]byte(html.UnescapeString(string(document[valueStart:valueEnd]))), &configuration); err != nil {
		return nil, errors.New("the VS Code web configuration is not valid")
	}
	configuration["connectionToken"] = connectionToken
	encoded, err := json.Marshal(configuration)
	if err != nil {
		return nil, err
	}
	escaped := []byte(html.EscapeString(string(encoded)))
	result := make([]byte, 0, len(document)+len(escaped)-(valueEnd-valueStart))
	result = append(result, document[:valueStart]...)
	result = append(result, escaped...)
	result = append(result, document[valueEnd:]...)
	return result, nil
}

func validEditorBasePath(path string) bool {
	const prefix = "/seizen/"
	if !strings.HasPrefix(path, prefix) || len(path) != len(prefix)+43 {
		return false
	}
	for _, character := range path[len(prefix):] {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '_' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func editorGatewayPathAllowed(path, basePath string) bool {
	return path == basePath || strings.HasPrefix(path, basePath+"/")
}
