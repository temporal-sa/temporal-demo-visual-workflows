package app

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSignedInUsersAreKeyedByEmail(t *testing.T) {
	s := NewServer(Config{TaskQueue: "visual-workflows-ab12", LibraryScope: "visual-workflows", TrustAuthHeaders: true, SessionSecret: []byte("x")},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	who := func(headers map[string]string) user {
		r := httptest.NewRequest(http.MethodGet, "/api/session", nil)
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		return s.identify(httptest.NewRecorder(), r)
	}

	// The same email, from any browser and however it's cased, is one user.
	a := who(map[string]string{"X-Temporal-Auth-Email": "Ada@Temporal.io", "X-Temporal-Auth-Subject": "google:1"})
	b := who(map[string]string{"X-Temporal-Auth-Email": "ada@temporal.io", "X-Temporal-Auth-Subject": "google:2"})
	require.True(t, a.SignedIn)
	require.Equal(t, a.Key, b.Key)
	require.Equal(t, "ada@temporal.io", a.Name)

	// Without the email header, the token's email claim is used.
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"email":"ada@temporal.io","sub":"google:1"}`))
	require.Equal(t, a.Key, who(map[string]string{"X-Temporal-Auth-JWT": "h." + payload + ".sig"}).Key)

	// No identity: an anonymous session, different each time.
	anon := who(nil)
	require.False(t, anon.SignedIn)
	require.NotEqual(t, anon.Key, who(nil).Key)

	// A crashable workspace opens the user's library on the base queue.
	require.Equal(t, "vflow-visual-workflows-"+a.Key+"-library", s.libraryID(a.Key))
}

func TestSessionNamesTheUINamespace(t *testing.T) {
	s := NewServer(Config{Namespace: "default", UINamespace: "demo.acct", TemporalUIURL: "https://cloud.temporal.io"},
		nil, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	w := httptest.NewRecorder()
	s.getSession(w, httptest.NewRequest(http.MethodGet, "/api/session", nil), user{})
	var got map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	require.Equal(t, "demo.acct", got["namespace"])
	require.Equal(t, "https://cloud.temporal.io", got["temporalUiUrl"])
}
