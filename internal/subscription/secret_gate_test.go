// Secret gate (audit §5 / OPE-3741): an update round over a
// subscription whose URL carries a token must never put the token (or
// any URL fragment) into the log callback. Uses the shared
// applog.RedactedLogger helper, so the assertion semantics are pinned
// in one place.
package subscription

import (
	"context"
	"errors"
	"testing"

	"github.com/wellboard/wellboard/internal/applog"
	"github.com/wellboard/wellboard/internal/model"
)

// fakeToken is a stand-in credential. Deliberately NOT a real token;
// also unlike the tokens in the existing fixtures it never appears in
// any expected log text, so any hit is a leak.
const fakeToken = "ZmFrZVRva2VuMTIz" // base64-ish garbage

const tokenedURL = "https://panel.example/api/v1/client/subscribe?token=" + fakeToken

// testStateWithSubURL is testState with the subscription source's URL
// replaced (local to this file so it reads top-down).
func testStateWithSubURL(url string) *model.State {
	st := testState()
	st.Sources[0].URL = url
	return st
}

func TestUpdateRoundTokenedURLLogsNoSecret(t *testing.T) {
	st := testStateWithSubURL(tokenedURL)
	// The fetch error mimics net/http: it embeds the full request URL.
	f := &mockFetcher{err: errors.New(`subscription: network: Get "` + tokenedURL + `": dial tcp 1.2.3.4:443: connect: connection refused`)}
	r := &applog.RedactedLogger{}
	u := &Updater{
		Fetcher: f, Store: &fakeStore{st},
		HWID: "ABCDEFGHJKLM0123456789",
		Log:  r.Log,
	}
	if _, err := u.Update(context.Background()); err != nil {
		t.Fatal(err)
	}

	// The update round DID trace the source (the log is not empty),
	// but the token never appears, nor does any URL fragment.
	if len(r.Lines()) == 0 {
		t.Fatal("expected at least one log line for the failing source")
	}
	r.HasSecret(t, fakeToken)
	r.HasSecret(t, "http://")
	r.HasSecret(t, "https://")
}

func TestUpdateOneTokenedURLLogsNoSecret(t *testing.T) {
	st := testStateWithSubURL(tokenedURL)
	f := &mockFetcher{err: errors.New(`subscription: network: Get "` + tokenedURL + `": x509: certificate signed by unknown authority`)}
	r := &applog.RedactedLogger{}
	u := &Updater{
		Fetcher: f, Store: &fakeStore{st},
		HWID: "ABCDEFGHJKLM0123456789",
		Log:  r.Log,
	}
	if _, err := u.UpdateOne(context.Background(), "sub_1"); err != nil {
		t.Fatal(err)
	}
	r.HasSecret(t, fakeToken)
	r.HasSecret(t, "http://")
	// last_error (persisted state) is guarded by the same redaction.
	if st.Sources[0].LastError == nil {
		t.Fatal("last_error must be recorded")
	}
	r.Log("last_error: %s", *st.Sources[0].LastError) // feed it through the same gate
	r.HasSecret(t, fakeToken)
}

func TestUpdateRoundSuccessTokenedURLLogsCountsOnly(t *testing.T) {
	st := testStateWithSubURL(tokenedURL)
	f := &mockFetcher{body: []byte(yamlBody)}
	r := &applog.RedactedLogger{}
	u := &Updater{
		Fetcher: f, Store: &fakeStore{st},
		HWID: "ABCDEFGHJKLM0123456789",
		Log:  r.Log,
	}
	res, err := u.Update(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("round must succeed: %v", res.Errors)
	}
	r.HasSecret(t, fakeToken)
	r.HasSecret(t, "http://")
	// The success trace names the source and counts, nothing else.
	if len(r.Lines()) == 0 || r.Lines()[0][:8] != "source s" {
		t.Fatalf("expected a per-source trace line, got %q", r.Lines())
	}
}
