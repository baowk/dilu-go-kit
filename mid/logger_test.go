package mid

import (
	"net/url"
	"strings"
	"testing"
)

func TestLogPathRedactsSensitiveQueryValues(t *testing.T) {
	u, err := url.Parse("/ws?token=secret&access_token=a&id_token=b&workspace_id=7")
	if err != nil {
		t.Fatal(err)
	}

	got := logPath(u)

	for _, secret := range []string{"secret", "access_token=a", "id_token=b"} {
		if strings.Contains(got, secret) {
			t.Fatalf("log path leaked sensitive query value: %q", got)
		}
	}
	if !strings.Contains(got, "workspace_id=7") {
		t.Fatalf("log path dropped non-sensitive query value: %q", got)
	}
	if !strings.Contains(got, "token=%5BREDACTED%5D") {
		t.Fatalf("log path did not redact token: %q", got)
	}
}
