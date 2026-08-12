package webui

import (
	"strings"
	"testing"
	"time"

	"github.com/plecture/plect/app/internal/domain"
	"github.com/plecture/plect/app/internal/service"
)

// Now that the detail page exists, each session name links to it. Names contain
// "/", so the href must be the path-escaped /sessions/<owner>/<name> form.
func TestRows_SessionNameLinksToDetail(t *testing.T) {
	now := time.Now()
	svc := &fakeService{entries: []service.ListEntry{
		{SessionName: "owner/repo-1", Run: domain.RunUp, Health: domain.HealthHealthy, LastActiveAt: &now},
	}}
	body := get(t, svc, "/sessions").Body.String()
	if !strings.Contains(body, "owner/repo-1") {
		t.Fatal("session name missing from rows")
	}
	if !strings.Contains(body, `href="/sessions/owner/repo-1"`) {
		t.Errorf("session name should link to its detail page; body:\n%s", body)
	}
}
