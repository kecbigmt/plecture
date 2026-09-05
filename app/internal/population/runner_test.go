package population

import (
	"context"
	"strings"
	"testing"

	"github.com/kecbigmt/plecture/app/internal/config"
	"github.com/kecbigmt/plecture/app/internal/lang"
)

func queryAction(command string, args ...string) *lang.Action {
	values := make([]*lang.Value, 0, len(args))
	for _, arg := range args {
		values = append(values, &lang.Value{Form: lang.FormLiteral, Literal: arg})
	}
	return &lang.Action{Type: lang.ActionExec, Command: command, Args: values}
}

func TestActionRunnerPollRequiresAJSONArray(t *testing.T) {
	runner := actionRunner{cfg: &config.Config{}}
	def := Definition{Observer: config.ResourceDef{Query: &config.ResourceQuery{}}}
	for _, output := range []string{`{"resource":"urn:case:a"}`, `null`} {
		def.Observer.Query.Poll = queryAction("printf", output)
		_, err := runner.Poll(context.Background(), def)
		if err == nil || !strings.Contains(err.Error(), "JSON item array") {
			t.Fatalf("output %s: error = %v, want JSON item array failure", output, err)
		}
	}

	def.Observer.Query.Poll = queryAction("printf", `[{"resource":"urn:case:a"}]`)
	items, err := runner.Poll(context.Background(), def)
	if err != nil || len(items) != 1 || items[0]["resource"] != "urn:case:a" {
		t.Fatalf("items = %v, error = %v", items, err)
	}
}

func TestActionRunnerSubscribeEmitsOneItemPerLine(t *testing.T) {
	runner := actionRunner{cfg: &config.Config{}}
	def := Definition{Observer: config.ResourceDef{Query: &config.ResourceQuery{
		Subscribe: queryAction("printf", "%s\n%s\n", `{"resource":"urn:case:a"}`, `{"resource":"urn:case:b"}`),
	}}}
	var resources []string
	err := runner.Subscribe(context.Background(), def, func(item map[string]any) error {
		resources = append(resources, item["resource"].(string))
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "exited") {
		t.Fatalf("termination error = %v, want supervised-restart signal", err)
	}
	if strings.Join(resources, ",") != "urn:case:a,urn:case:b" {
		t.Fatalf("resources = %v", resources)
	}
}
