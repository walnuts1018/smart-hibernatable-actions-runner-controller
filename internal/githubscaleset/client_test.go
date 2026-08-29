package githubscaleset

import (
	"context"
	"testing"

	"github.com/actions/scaleset"
)

func TestNewClient_NilAuth(t *testing.T) {
	_, err := NewClient("https://github.com/walnuts1018/test", nil)
	if err == nil {
		t.Fatal("expected error when auth is nil, got nil")
	}
	expectedMsg := "GitHub App auth is required"
	if err.Error() != expectedMsg {
		t.Fatalf("expected error %q, got %q", expectedMsg, err.Error())
	}
}

func TestScaleSetClientFactory(t *testing.T) {
	factory := NewScaleSetClientFactory()
	if factory == nil {
		t.Fatal("expected non-nil factory")
	}

	_, err := factory.NewClient("https://github.com/walnuts1018/test", nil)
	if err == nil {
		t.Fatal("expected error with nil auth")
	}
}

func TestListenerSessionImpl_CloseNilSessionClient(t *testing.T) {
	sess := &listenerSessionImpl{
		sessionClient: nil,
	}
	if err := sess.Close(context.Background()); err != nil {
		t.Fatalf("expected nil error when sessionClient is nil, got: %v", err)
	}
}

func TestScaleSetClient_JITConfigResponse(t *testing.T) {
	resp := &JITConfigResponse{
		RunnerID:         42,
		RunnerName:       "test-runner-42",
		EncodedJITConfig: "dummy-encoded-config",
	}

	if resp.RunnerID != 42 || resp.RunnerName != "test-runner-42" || resp.EncodedJITConfig != "dummy-encoded-config" {
		t.Errorf("unexpected values in JITConfigResponse: %+v", resp)
	}
}

func TestScaleSetClient_ValidAuthCreation(t *testing.T) {
	auth := &scaleset.GitHubAppAuth{
		ClientID:       "12345",
		InstallationID: 67890,
		PrivateKey:     validTestRSAPrivateKeyPEM,
	}

	client, err := NewClient("https://github.com/walnuts1018/test", auth)
	if err != nil {
		t.Fatalf("unexpected error creating client with valid auth: %v", err)
	}
	if client == nil {
		t.Fatal("expected client to be non-nil")
	}
}

const validTestRSAPrivateKeyPEM = `-----BEGIN PRIVATE KEY-----
MIIEvwIBADANBgkqhkiG9w0BAQEFAASCBKkwggSlAgEAAoIBAQC11udPh2QLYPFH
QOvdv7G6yxfheEbBTcj2FsgfqaJGpBb8TlEe0nD3GHe4TJKxdOUbDtOQpbBqFIpX
PVrYUuvFEWlvSvabwWrYbbTnW7yg+YO5HUf4rMmyGt8ZtPvvuymZzBOPk+yfx6Ld
iBIUEuqa1bxk7gahcx+zlxfPqg471Q7zo0BqUy1IgBP2QqPnWZFlP94fg3xbbrGa
LZj1OiAVIZmQu2QaTP75vye/XrTGDD5UtQRg/d/LcAv7iddP68KbT3o0xlryjjzp
n15YqAib0YvWosAde+Z/O2pV6gpn6pvq2trDuC09++SysgWRA9g0IWLWY7X5R1/H
AZDxyaNBAgMBAAECggEAO0yFyk2gtoU6qb3mLT5iO0QX2ZNbn5Y6PuZXBNxQ6zB/
vm/bzG1cIXh9MkDmZbB1NkmzfKxLx4xDQQflJD6GXJG9DGop2clNip7cK8ai0OwN
pMSDv/i5HbfdoYh/0EH84wbGKkBXHhQAbLX/D0TL9QpWkaN9zhC4+dwAC9ytH51i
axShF6CHgydYxNZu/bf5XuNXXlY80LJ+Ud0rd1lXKFbz5fYxgs43RkFg9CWWqqDC
rRweNs8evqzATOrD7REccX/QTUJjgXvRaGlVfiyJQHAUyD7vbY0IqgdWJRlTC3r9
MwJri5QjK+UVdxWGcMdvO++su0mhrpSNnM6II4+68QKBgQDmJrvVtuCzbtAjlvGO
jHQhwOYlysH3gcccwS/Y6RV0Y61DZm7v2s0kp85wtO73iuHnC2KwwXjgi42R0VFr
gAWWkEJ8MddX9LwL62+/bnv2A/ajVWIdY1wUovHRdTx3W3qJWZ+684aC+nFRsgtz
miBBsyfNPmGz2ZLYMC3mbS82FwKBgQDKQx384YUsI5VAk6W/fiw2+W+KmhEpKD2P
t3VHD3zNBUb1wMTReZ8RORDHQ9qZiIEMU+zCRHfyMhd7PzexPYMRzavBPqYsrW5O
Q93n8LZC1H9InWGCC2Elk3WgliGqkHZLCHGF5GrK5G49w1V7m/ms+4zD/HQT8evd
9dY9+DggZwKBgQDYXhnAdUkR51+t1b4KMWkMQnkbll57/XnfQo9k8NvGq967upUY
0S6DA29E7hSqi9qMh1ukqH6nOwtAxvQwiA642a5na8PzYJVY72IDKi9HvbolG6Q9
1KdAj1+fdwP9gfbVIXjVHRScFi5qi2PQrlkc6vzEK51Wo3k13TWJp6P2yQKBgQCo
eWF4K21fB8ChipqMOA+iNwEG5TAYJTGqDTk92JOuvo+N0mTeyzyI/wyPvmBOdNpx
J1LVumxirADNIypDkyYi5TsEeye1nTx9KqCjOujGH/RpytXWmZ3wy7Q17/fY9/3g
oAbXbRzbJY0CGzuP+6rrwJhPA3C40FEUkFpFQgWWTwKBgQCyqjpc74Kvc0TFrZ3d
uOIW8u0YbaViHk0OV+qO1HjUXDFAESwOGJ8nu92Rf2rui4Wbw4JD4s4s9kofQh/e
YbdWk16w6qaQF2QKr0hUHNyFy5027uabeQulxMRBxEt5kfoS9ul9CqSePkoH99WB
83Az7XN4r9nFbKH6NdKHUw6GOQ==
-----END PRIVATE KEY-----`
