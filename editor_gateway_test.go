package main

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

type editorRoundTripFunc func(*http.Request) (*http.Response, error)

func (function editorRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestEditorGatewayBootstrapsPrivateCookieWithoutNetwork(t *testing.T) {
	upstream, _ := url.Parse("http://127.0.0.1:49123")
	basePath := "/seizen/" + strings.Repeat("a", 43)
	requests := 0
	transport := editorRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		requests++
		if requests == 1 {
			if request.URL.Query().Get("tkn") != "connection-secret" {
				t.Fatalf("missing bootstrap token")
			}
			return &http.Response{
				StatusCode: http.StatusFound,
				Header: http.Header{
					"Location":   []string{basePath + "/"},
					"Set-Cookie": []string{"vscode-tkn=session-secret; Path=" + basePath + "; HttpOnly"},
				},
				Body: io.NopCloser(strings.NewReader("")),
			}, nil
		}
		if request.URL.Query().Get("tkn") != "" || request.Header.Get("Cookie") != "vscode-tkn=session-secret" {
			t.Fatalf("redirect was not authenticated: %s %q", request.URL.String(), request.Header.Get("Cookie"))
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("ready"))}, nil
	})

	jar, err := bootstrapEditorGateway(context.Background(), transport, upstream, basePath, "connection-secret")
	if err != nil {
		t.Fatal(err)
	}
	cookieURL, _ := url.Parse(upstream.String() + basePath + "/")
	cookies := jar.Cookies(cookieURL)
	if requests != 2 || len(cookies) != 1 || cookies[0].Name != "vscode-tkn" || cookies[0].Value != "session-secret" {
		t.Fatalf("unexpected bootstrap result: requests=%d cookies=%v", requests, cookies)
	}
}

func TestEditorGatewayRejectsBootstrapRedirectOutsideUpstream(t *testing.T) {
	upstream, _ := url.Parse("http://127.0.0.1:49123")
	basePath := "/seizen/" + strings.Repeat("d", 43)
	transport := editorRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{"http://example.com/steal"}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	})
	if _, err := bootstrapEditorGateway(context.Background(), transport, upstream, basePath, "connection-secret"); !errors.Is(err, errEditorGatewayRedirect) {
		t.Fatalf("external redirect was not rejected: %v", err)
	}
}

func TestEditorGatewayGuardsPrefixAndKeepsAuthenticationBackendOnly(t *testing.T) {
	upstream, _ := url.Parse("http://127.0.0.1:49123")
	basePath := "/seizen/" + strings.Repeat("b", 43)
	var forwarded *http.Request
	transport := editorRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		forwarded = request.Clone(request.Context())
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Location":   []string{"http://127.0.0.1:49123" + basePath + "/workbench?tkn=must-not-leak"},
				"Set-Cookie": []string{"vscode-tkn=must-not-reach-browser; Path=" + basePath},
			},
			Body:    io.NopCloser(strings.NewReader("")),
			Request: request,
		}, nil
	})
	jar := editorTestCookieJar(upstream, basePath, "backend-only")
	handler := newEditorGatewayHandler(
		"127.0.0.1:48200",
		"http://127.0.0.1:48200",
		upstream,
		basePath,
		"process-secret",
		jar,
		transport,
	)

	denied := httptest.NewRecorder()
	handler.ServeHTTP(denied, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:48200/not-secret", nil))
	if denied.Code != http.StatusNotFound || forwarded != nil {
		t.Fatalf("request outside the secret prefix was forwarded: %d", denied.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:48200"+basePath+"/workbench?tkn=browser-value&view=files", nil)
	request.Header.Set("Cookie", "browser=ignored")
	request.Header.Set("Origin", "http://127.0.0.1:48200")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if forwarded == nil || forwarded.Host != upstream.Host || forwarded.URL.Host != upstream.Host {
		t.Fatalf("request was not sent to the private upstream: %#v", forwarded)
	}
	if forwarded.Header.Get("Cookie") != "vscode-tkn=backend-only" || forwarded.URL.Query().Get("tkn") != "" || forwarded.URL.Query().Get("view") != "files" {
		t.Fatalf("authentication was not isolated: %q %q", forwarded.Header.Get("Cookie"), forwarded.URL.RawQuery)
	}
	if forwarded.Header.Get("Origin") != "http://127.0.0.1:49123" {
		t.Fatalf("origin was not rewritten: %q", forwarded.Header.Get("Origin"))
	}
	if response.Header().Get("Set-Cookie") != "" || strings.Contains(response.Header().Get("Location"), "tkn=") {
		t.Fatalf("upstream credentials leaked: headers=%v", response.Header())
	}
	if response.Header().Get("Location") != "http://127.0.0.1:48200"+basePath+"/workbench" {
		t.Fatalf("location was not rewritten: %q", response.Header().Get("Location"))
	}
	privateURL, _ := url.Parse(upstream.String() + basePath + "/workbench")
	if refreshed := editorCookieValue(jar.Cookies(privateURL), "vscode-tkn"); refreshed != "must-not-reach-browser" {
		t.Fatalf("upstream cookie was not refreshed privately: %q", refreshed)
	}
}

func TestEditorGatewayUsesAuthenticationForWebSocketUpgrade(t *testing.T) {
	upstream, _ := url.Parse("http://127.0.0.1:49123")
	basePath := "/seizen/" + strings.Repeat("c", 43)
	var cookie, origin string
	transport := editorRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		cookie, origin = request.Header.Get("Cookie"), request.Header.Get("Origin")
		return &http.Response{StatusCode: http.StatusBadGateway, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("")), Request: request}, nil
	})
	jar := editorTestCookieJar(upstream, basePath, "backend-only")
	handler := newEditorGatewayHandler("127.0.0.1:48200", "http://127.0.0.1:48200", upstream, basePath, "process-secret", jar, transport)
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:48200"+basePath+"/socket", nil)
	request.Header.Set("Connection", "Upgrade")
	request.Header.Set("Upgrade", "websocket")
	request.Header.Set("Origin", "http://127.0.0.1:48200")
	handler.ServeHTTP(httptest.NewRecorder(), request)
	if cookie != "vscode-tkn=backend-only" || origin != "http://127.0.0.1:49123" {
		t.Fatalf("websocket authentication was not injected: cookie=%q origin=%q", cookie, origin)
	}
}

func TestEditorGatewayRejectsExternalUpstreamRedirect(t *testing.T) {
	upstream, _ := url.Parse("http://127.0.0.1:49123")
	basePath := "/seizen/" + strings.Repeat("e", 43)
	transport := editorRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header: http.Header{
				"Location":   []string{"//example.com/steal"},
				"Set-Cookie": []string{"vscode-tkn=must-not-leak; Path=/"},
			},
			Body:    io.NopCloser(strings.NewReader("")),
			Request: request,
		}, nil
	})
	handler := newEditorGatewayHandler("127.0.0.1:48200", "http://127.0.0.1:48200", upstream, basePath, "process-secret", editorTestCookieJar(upstream, basePath, "backend-only"), transport)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:48200"+basePath+"/", nil))
	if response.Code != http.StatusBadGateway || response.Header().Get("Location") != "" || response.Header().Get("Set-Cookie") != "" {
		t.Fatalf("external redirect escaped gateway: code=%d headers=%v", response.Code, response.Header())
	}
}

func TestEditorGatewayProvidesWorkbenchWebSocketAuthentication(t *testing.T) {
	upstream, _ := url.Parse("http://127.0.0.1:49123")
	basePath := "/seizen/" + strings.Repeat("f", 43)
	configuration := html.EscapeString(`{"remoteAuthority":"127.0.0.1:48200"}`)
	document := `<html><head><meta id="vscode-workbench-web-configuration" data-settings="` + configuration + `"></head></html>`
	transport := editorRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"text/html; charset=utf-8"},
				"Set-Cookie":   []string{"vscode-tkn=must-stay-private; Path=" + basePath},
			},
			Body:    io.NopCloser(strings.NewReader(document)),
			Request: request,
		}, nil
	})
	handler := newEditorGatewayHandler("127.0.0.1:48200", "http://127.0.0.1:48200", upstream, basePath, "process-secret", editorTestCookieJar(upstream, basePath, "backend-only"), transport)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "http://127.0.0.1:48200"+basePath+"/", nil))
	if response.Code != http.StatusOK || response.Header().Get("Set-Cookie") != "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("unexpected workbench response: code=%d headers=%v", response.Code, response.Header())
	}
	marker := editorWorkbenchConfigurationMarker
	start := strings.Index(response.Body.String(), marker)
	if start < 0 {
		t.Fatal("missing workbench configuration")
	}
	encoded := response.Body.String()[start+len(marker):]
	end := strings.IndexByte(encoded, '"')
	if end < 0 {
		t.Fatal("invalid workbench configuration")
	}
	var actual map[string]any
	if err := json.Unmarshal([]byte(html.UnescapeString(encoded[:end])), &actual); err != nil {
		t.Fatal(err)
	}
	if actual["connectionToken"] != "process-secret" || actual["remoteAuthority"] != "127.0.0.1:48200" {
		t.Fatalf("unexpected workbench configuration: %#v", actual)
	}
}

func editorTestCookieJar(upstream *url.URL, basePath, value string) *cookiejar.Jar {
	jar, _ := cookiejar.New(nil)
	cookieURL, _ := url.Parse(upstream.String() + basePath + "/")
	jar.SetCookies(cookieURL, []*http.Cookie{{Name: "vscode-tkn", Value: value, Path: basePath}})
	return jar
}

func editorCookieValue(cookies []*http.Cookie, name string) string {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie.Value
		}
	}
	return ""
}

func TestEditorGatewayPathContract(t *testing.T) {
	valid := "/seizen/" + strings.Repeat("Z", 43)
	if !validEditorBasePath(valid) || !editorGatewayPathAllowed(valid+"/asset.js", valid) || editorGatewayPathAllowed(valid+"-other", valid) {
		t.Fatal("private gateway path contract is not enforced")
	}
}
