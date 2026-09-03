package protocol_test

import (
	"encoding/json"
	"testing"

	"github.com/mhd64real/printer-cycle/internal/jsonrpc"
	"github.com/mhd64real/printer-cycle/internal/store"
)

// submitFor opens a stream with whatever attribution the caller wants, and
// returns the job id.
func submitFor(t *testing.T, c *client, printerID string, extra map[string]any) (string, uint32, *jsonrpc.Error) {
	t.Helper()

	params := map[string]any{
		"printer_id": printerID,
		"document":   map[string]any{"filename": "who.txt", "mime": "text/plain"},
	}
	for k, v := range extra {
		params[k] = v
	}

	resp := c.call("jobs.submit", params)
	if resp.Error != nil {
		return "", 0, resp.Error
	}
	var opened struct {
		JobID    string `json:"job_id"`
		StreamID uint32 `json:"stream_id"`
	}
	if err := json.Unmarshal(resp.Result, &opened); err != nil {
		t.Fatal(err)
	}
	return opened.JobID, opened.StreamID, nil
}

// The stage's done-when: a connector cannot claim a person it has no link with.
func TestAForgedIdentityIsRefused(t *testing.T) {
	url, db := cupsBackedServer(t)

	if _, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2"); err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())
	printerID, _ := addTestPrinterNamed(t, dashboard)

	// A connector that says it identifies people, but has no link for this one.
	telegram := authedClient(t, url, db, "telegram",
		[]string{store.ScopeJobsSubmit, store.ScopeIdentityLink})
	if resp := telegram.call("register", map[string]any{
		"name": "Telegram", "identity": "linked",
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	_, _, rpcErr := submitFor(t, telegram, printerID, map[string]any{"on_behalf_of": "tg:nobody"})
	if rpcErr == nil {
		t.Fatal("a connector submitted for an identity it has no link with")
	}
	if rpcErr.Code != jsonrpc.CodeIdentityNotLinked {
		t.Errorf("code = %d, want %d", rpcErr.Code, jsonrpc.CodeIdentityNotLinked)
	}
}

// A connector that declared it does not identify people must not then claim to.
// The declaration is not decoration: an administrator chose a fallback user on
// the strength of it.
func TestAConnectorThatSaysItKnowsNobodyCannotNameSomebody(t *testing.T) {
	url, db := cupsBackedServer(t)

	user, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())
	printerID, _ := addTestPrinterNamed(t, dashboard)

	// AirPrint: authenticated to core, and knows nothing about who is printing.
	airprint := authedClient(t, url, db, "airprint",
		[]string{store.ScopeJobsSubmit, store.ScopeIdentityLink})
	if resp := airprint.call("register", map[string]any{
		"name": "AirPrint", "identity": "none",
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	// Even a real, linked identity is refused, because this connector said it
	// does not do that.
	if resp := airprint.call("identity.linkRequest", map[string]any{"external_id": "phone:1"}); resp.Error != nil {
		t.Fatal(resp.Error)
	}
	var issued struct {
		Code string `json:"code"`
	}
	resp := airprint.call("identity.linkRequest", map[string]any{"external_id": "phone:1"})
	json.Unmarshal(resp.Result, &issued)
	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")
	dashboard.call("identity.approve", map[string]any{"code": issued.Code, "session": session})

	_, _, rpcErr := submitFor(t, airprint, printerID, map[string]any{"on_behalf_of": "phone:1"})
	if rpcErr == nil {
		t.Fatal("a connector declaring identity:none named a person")
	}
	if rpcErr.Code != jsonrpc.CodeIdentityNotLinked {
		t.Errorf("code = %d, want %d", rpcErr.Code, jsonrpc.CodeIdentityNotLinked)
	}
	_ = user
}

// The Telegram path: a link core owns, so nothing is taken on trust.
func TestALinkedIdentityAttributesTheJob(t *testing.T) {
	url, db := cupsBackedServer(t)

	user, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())
	printerID, _ := addTestPrinterNamed(t, dashboard)

	telegram := authedClient(t, url, db, "telegram",
		[]string{store.ScopeJobsSubmit, store.ScopeIdentityLink})
	if resp := telegram.call("register", map[string]any{
		"name": "Telegram", "identity": "linked",
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	resp := telegram.call("identity.linkRequest", map[string]any{"external_id": "tg:887312"})
	var issued struct {
		Code string `json:"code"`
	}
	json.Unmarshal(resp.Result, &issued)

	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")
	if resp := dashboard.call("identity.approve", map[string]any{
		"code": issued.Code, "session": session,
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	jobID, _, rpcErr := submitFor(t, telegram, printerID, map[string]any{"on_behalf_of": "tg:887312"})
	if rpcErr != nil {
		t.Fatalf("submitting for a linked identity: %v", rpcErr)
	}

	job, err := db.Job(ctx(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.UserID != user.ID {
		t.Errorf("job belongs to %q, want %q", job.UserID, user.ID)
	}
}

// The dashboard path: a session, which is proof rather than a claim.
func TestASessionAttributesTheJob(t *testing.T) {
	url, db := cupsBackedServer(t)

	user, err := db.CreateUser(ctx(), "mohamed", "", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())
	printerID, _ := addTestPrinterNamed(t, dashboard)
	session := signIn(t, dashboard, "mohamed", "hunter2hunter2")

	jobID, _, rpcErr := submitFor(t, dashboard, printerID, map[string]any{"session": session})
	if rpcErr != nil {
		t.Fatalf("submitting with a session: %v", rpcErr)
	}

	job, err := db.Job(ctx(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.UserID != user.ID {
		t.Errorf("job belongs to %q, want the signed-in user %q", job.UserID, user.ID)
	}

	// A made-up session is refused rather than quietly ignored.
	_, _, rpcErr = submitFor(t, dashboard, printerID, map[string]any{"session": "invented"})
	if rpcErr == nil {
		t.Error("a made-up session was accepted")
	}
}

// The AirPrint path: nobody named, so the job belongs to whoever an
// administrator nominated.
func TestAnUnidentifiedJobGoesToTheFallbackUser(t *testing.T) {
	url, db := cupsBackedServer(t)

	user, err := db.CreateUser(ctx(), "household", "", "hunter2hunter2")
	if err != nil {
		t.Fatal(err)
	}

	dashboard := authedClient(t, url, db, "dashboard", store.KnownScopes())
	printerID, _ := addTestPrinterNamed(t, dashboard)

	airprint := authedClient(t, url, db, "airprint", []string{store.ScopeJobsSubmit})
	if resp := airprint.call("register", map[string]any{
		"name": "AirPrint", "identity": "none",
	}); resp.Error != nil {
		t.Fatal(resp.Error)
	}

	// Before a fallback is chosen, the job belongs to nobody in particular,
	// which is honest rather than wrong.
	jobID, _, rpcErr := submitFor(t, airprint, printerID, nil)
	if rpcErr != nil {
		t.Fatalf("submitting without naming anybody: %v", rpcErr)
	}
	job, err := db.Job(ctx(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.UserID != "" {
		t.Errorf("job belongs to %q with no fallback set", job.UserID)
	}

	// Once an administrator nominates somebody, jobs go to them.
	if resp := dashboard.call("connectors.setFallbackUser", map[string]any{
		"connector_id": "airprint", "user_id": user.ID,
	}); resp.Error != nil {
		t.Fatalf("setting a fallback: %v", resp.Error)
	}

	jobID, _, rpcErr = submitFor(t, airprint, printerID, nil)
	if rpcErr != nil {
		t.Fatal(rpcErr)
	}
	job, err = db.Job(ctx(), jobID)
	if err != nil {
		t.Fatal(err)
	}
	if job.UserID != user.ID {
		t.Errorf("job belongs to %q, want the fallback user %q", job.UserID, user.ID)
	}
}
