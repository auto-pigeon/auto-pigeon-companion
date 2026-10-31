package web

import (
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/auto-pigeon/auto-pigeon-companion/internal/managedtools"
)

// HITL, 2026-10-31: the page asks, and nothing is downloaded before a person
// presses Install.
func TestNothingIsInstalledBeforeAPersonApproves(t *testing.T) {
	m := newToolsMachineAsking(t)
	m.step()
	tool := toolIn(t, m.server.toolsStatus().Status, managedtools.EricW)
	if !tool.AwaitingInstall || tool.Active != nil {
		t.Fatalf("before approval: %+v", tool)
	}
	if m.server.toolsStatus().SettingUp {
		t.Fatal("the page says it is setting up while it has not been asked to")
	}
	if status, body := m.call(http.MethodPost, "/api/v1/tools/install", map[string]any{"tools": []string{"companion"}}); status != http.StatusBadRequest {
		t.Fatalf("the Companion itself = %d %v", status, body)
	}
	if status, body := m.call(http.MethodPost, "/api/v1/tools/install", map[string]any{"tools": []string{"ericw-tools"}}); status != http.StatusOK {
		t.Fatalf("install = %d %v", status, body)
	}
	m.step()
	tool = toolIn(t, m.server.toolsStatus().Status, managedtools.EricW)
	if tool.AwaitingInstall || tool.Active == nil {
		t.Fatalf("after approval: %+v", tool)
	}
	if other := toolIn(t, m.server.toolsStatus().Status, managedtools.Q3Map2); !other.AwaitingInstall || other.Active != nil {
		t.Fatalf("approving one approved the other: %+v", other)
	}
}

// The notices the page draws, read from the assets it serves.
func TestTheBuildRequestNoticeOpensTheFormByItself(t *testing.T) {
	raw, err := os.ReadFile("assets/buildrequest.js")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	if strings.Contains(source, `t("Review build")`) || strings.Contains(source, `t("Go to the review")`) {
		t.Fatal("the notice still offers a review")
	}
	if !strings.Contains(source, "opened = seen.request_id; review();") {
		t.Fatal("the notice does not open the form")
	}
}
