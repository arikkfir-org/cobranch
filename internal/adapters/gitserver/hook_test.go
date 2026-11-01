package gitserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"cobranch.dev/internal/services/git"
)

func TestParseUpdates(t *testing.T) {
	tests := []struct {
		name    string
		lines   string
		want    []git.RefUpdate
		wantErr bool
	}{
		{name: "two refs", lines: git.ZeroSHA + " " + parent + " refs/heads/main\n" + parent + " " + git.ZeroSHA + " refs/tags/v1\n",
			want: []git.RefUpdate{{Name: "refs/heads/main", Old: git.ZeroSHA, New: parent}, {Name: "refs/tags/v1", Old: parent, New: git.ZeroSHA}}},
		{name: "none", lines: "\n"},
		{name: "a line that isn't one", lines: "refs/heads/main\n", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseUpdates(tt.lines)
			if (err != nil) != tt.wantErr || len(got) != len(tt.want) {
				t.Fatalf("parseUpdates() = %+v, %v", got, err)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("update %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestHookRequestEnv(t *testing.T) {
	dir := "/data/work/repo-1"
	tests := []struct {
		name    string
		hr      hookRequest
		want    []string
		wantErr bool
	}{
		{name: "the quarantine and the repository's objects", hr: hookRequest{Quarantine: dir + "/objects/tmp_objdir-incoming-x", ObjectDirectory: dir + "/objects/tmp_objdir-incoming-x",
			Alternates: dir + "/objects"}, want: []string{"GIT_ALTERNATE_OBJECT_DIRECTORIES=" + dir + "/objects", "GIT_OBJECT_DIRECTORY=" + dir + "/objects/tmp_objdir-incoming-x",
			"GIT_QUARANTINE_PATH=" + dir + "/objects/tmp_objdir-incoming-x"}},
		{name: "nothing", hr: hookRequest{}},
		{name: "a quarantine outside the repository", hr: hookRequest{Quarantine: "/data/work/repo-2/objects"}, wantErr: true},
		{name: "a way out", hr: hookRequest{ObjectDirectory: dir + "/../repo-2/objects"}, wantErr: true},
		{name: "one alternate of two outside", hr: hookRequest{Alternates: dir + "/objects:/etc"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.hr.env(dir)
			if (err != nil) != tt.wantErr {
				t.Fatalf("env() = %v, %v", got, err)
			}
			if strings.Join(slices.Sorted(slices.Values(got)), " ") != strings.Join(tt.want, " ") {
				t.Errorf("env() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIdxObjects(t *testing.T) {
	idx := func(magic string, version, objects uint32) []byte {
		b := append([]byte(magic), make([]byte, 4+256*4)...)
		binary.BigEndian.PutUint32(b[4:8], version)
		binary.BigEndian.PutUint32(b[len(b)-4:], objects)
		return b
	}
	tests := []struct {
		name    string
		content []byte
		want    int64
		wantErr bool
	}{
		{name: "version 2", content: idx("\377tOc", 2, 42), want: 42},
		{name: "version 3", content: idx("\377tOc", 3, 42), wantErr: true},
		{name: "version 1, which has no header", content: idx("\000\000\000\001", 2, 42), wantErr: true},
		{name: "a short file", content: []byte("\377tOc"), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "pack.idx")
			if err := os.WriteFile(file, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}
			got, err := idxObjects(file)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("idxObjects() = %d, %v, want %d", got, err, tt.want)
			}
		})
	}
}

func TestProtocolEnv(t *testing.T) {
	tests := []struct {
		name, service, header string
		want                  []string
	}{
		{name: "a fetch in protocol v2", service: uploadPack, header: "version=2", want: []string{"GIT_PROTOCOL=version=2"}},
		{name: "a fetch in protocol v2 with more", service: uploadPack, header: "version=2:object-format=sha1", want: []string{"GIT_PROTOCOL=version=2:object-format=sha1"}},
		{name: "a fetch in protocol v0", service: uploadPack},
		{name: "a push, which speaks v0 alone", service: receivePack, header: "version=2"},
		{name: "a header that would add a variable", service: uploadPack, header: "version=2\nGIT_DIR=/etc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/acme/website.git/"+tt.service, nil)
			if tt.header != "" {
				r.Header.Set("Git-Protocol", tt.header)
			}
			if got := (&Server{}).protocolEnv(r, tt.service); strings.Join(got, " ") != strings.Join(tt.want, " ") {
				t.Errorf("protocolEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRequestBody(t *testing.T) {
	var zipped bytes.Buffer
	zw := gzip.NewWriter(&zipped)
	_, _ = zw.Write([]byte("0000"))
	_ = zw.Close()
	tests := []struct {
		name, contentType, encoding string
		body                        []byte
		wantStatus                  int
		want                        string
	}{
		{name: "plain", contentType: "application/x-git-upload-pack-request", body: []byte("0000"), want: "0000"},
		{name: "gzipped", contentType: "application/x-git-upload-pack-request", encoding: "gzip", body: zipped.Bytes(), want: "0000"},
		{name: "another service's body", contentType: "application/x-git-receive-pack-request", body: []byte("0000"), wantStatus: http.StatusUnsupportedMediaType},
		{name: "gzip that isn't", contentType: "application/x-git-upload-pack-request", encoding: "gzip", body: []byte("0000"), wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/acme/website.git/git-upload-pack", bytes.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			if tt.encoding != "" {
				r.Header.Set("Content-Encoding", tt.encoding)
			}
			w := httptest.NewRecorder()
			body, ok := requestBody(w, r, uploadPack)
			if tt.wantStatus != 0 {
				if ok || w.Code != tt.wantStatus {
					t.Errorf("requestBody() = %t with status %d, want %d", ok, w.Code, tt.wantStatus)
				}
				return
			}
			got, err := io.ReadAll(body)
			if !ok || err != nil || string(got) != tt.want {
				t.Errorf("requestBody() = %q, %t, %v", got, ok, err)
			}
		})
	}
}

func TestPktLine(t *testing.T) {
	if got := pktLine("# service=git-upload-pack\n"); got != "001e# service=git-upload-pack\n" {
		t.Errorf("pktLine() = %q", got)
	}
}

func TestHookHandler(t *testing.T) {
	s := &Server{pushes: map[string]*pending{}}
	token := s.expect(&pending{ctx: context.Background()})
	tests := []struct {
		name, token, body string
		want              int
	}{
		{name: "no token", body: "{}", want: http.StatusUnauthorized},
		{name: "a token of no push", token: "other", body: "{}", want: http.StatusUnauthorized},
		{name: "a body that isn't a hook's", token: token, body: "updates", want: http.StatusBadRequest},
		{name: "a line Git never gives", token: token, body: `{"updates": "main\n"}`, want: http.StatusConflict},
		{name: "a refused ref", token: token, body: `{"updates": "` + git.ZeroSHA + " " + parent + ` refs/notes/x\n"}`, want: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, hookPath, strings.NewReader(tt.body))
			if tt.token != "" {
				r.Header.Set("Authorization", "Bearer "+tt.token)
			}
			w := httptest.NewRecorder()
			s.hookHandler().ServeHTTP(w, r)
			if w.Code != tt.want {
				t.Errorf("status = %d, want %d: %s", w.Code, tt.want, w.Body)
			}
		})
	}
	s.forget(token)
	if len(s.pushes) != 0 {
		t.Error("forget() kept the push")
	}
}

func TestRunHook(t *testing.T) {
	updates := git.ZeroSHA + " " + parent + " refs/heads/main\n"
	tests := []struct {
		name       string
		status     int
		answer     string
		down       bool
		wantCode   int
		wantStderr string
	}{
		{name: "accepted", status: http.StatusOK, wantCode: 0},
		{name: "accepted with a note", status: http.StatusOK, answer: "Welcome\n", wantCode: 0, wantStderr: "Welcome\n"},
		{name: "refused", status: http.StatusConflict, answer: "refs/heads/main moved\n", wantCode: 1, wantStderr: "refs/heads/main moved\n"},
		{name: "a server that fails", status: http.StatusInternalServerError, answer: "boom\n", wantCode: 1, wantStderr: git.MessageNotRecorded + "\n"},
		{name: "a server that's gone", down: true, wantCode: 1, wantStderr: git.MessageNotRecorded + "\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got hookRequest
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer secret" {
					t.Errorf("the hook bore %q", r.Header.Get("Authorization"))
				}
				_ = json.NewDecoder(r.Body).Decode(&got)
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.answer)
			}))
			if tt.down {
				srv.Close()
			} else {
				defer srv.Close()
			}
			var stderr bytes.Buffer
			env := HookEnv{URL: srv.URL + hookPath, Token: "secret", Quarantine: "/q", ObjectDirectory: "/o", Alternates: "/a"}
			if code := RunHook(t.Context(), env, strings.NewReader(updates), &stderr); code != tt.wantCode || stderr.String() != tt.wantStderr {
				t.Errorf("RunHook() = %d printing %q, want %d printing %q", code, stderr.String(), tt.wantCode, tt.wantStderr)
			}
			if !tt.down && (got.Updates != updates || got.Quarantine != "/q" || got.ObjectDirectory != "/o" || got.Alternates != "/a") {
				t.Errorf("the hook sent %+v", got)
			}
		})
	}
}
