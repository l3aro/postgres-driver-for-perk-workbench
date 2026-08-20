package postgres

import (
	"context"
	"net/url"
	"reflect"
	"testing"

	"github.com/l3aro/perk-workbench-plugin-sdk-go/driver"
)

func TestFactoryCapabilities_preservePostgreSQLContract(t *testing.T) {
	capabilities := (Factory{}).Capabilities()
	if err := driver.ValidateCapabilities(capabilities); err != nil {
		t.Fatalf("ValidateCapabilities() = %v", err)
	}
	wantTargets := []driver.TargetPattern{
		{Prefix: "postgres://", KeepTarget: true},
		{Prefix: "postgresql://", KeepTarget: true},
		{Prefix: "postgres:"},
	}
	if !reflect.DeepEqual(capabilities.Targets, wantTargets) {
		t.Fatalf("targets = %#v, want %#v", capabilities.Targets, wantTargets)
	}
	if capabilities.Form == nil {
		t.Fatal("form is nil")
	}
	if got := capabilities.Form.Prefix; got != "postgres:" {
		t.Fatalf("form prefix = %q, want postgres:", got)
	}
	keys := make([]string, 0, len(capabilities.Form.Fields))
	for _, field := range capabilities.Form.Fields {
		keys = append(keys, field.Key)
	}
	if want := []string{"host", "port", "username", "password", "database", "tls"}; !reflect.DeepEqual(keys, want) {
		t.Fatalf("form keys = %#v, want %#v", keys, want)
	}
	if got := capabilities.Form.Fields[5].Options; !reflect.DeepEqual(got, []driver.FormOption{
		{Label: "Verify certificate", Value: "verify-full"},
		{Label: "Encrypt, don't verify certificate", Value: "require"},
		{Label: "Don't encrypt", Value: "disable"},
	}) {
		t.Fatalf("TLS options = %#v", got)
	}
	if !capabilities.WriteCapabilities.RowWriter {
		t.Fatal("row writer capability is false")
	}
}

func TestFactoryBuildTarget_serializesFormValues(t *testing.T) {
	result, err := (Factory{}).BuildTarget(context.Background(), driver.FormValues{
		Host: "db.example", Port: "5432", User: "alice", Pass: "secret", Database: "app", TLS: "require",
	})
	if err != nil {
		t.Fatalf("BuildTarget() = %v", err)
	}
	if !result.OK {
		t.Fatal("BuildTarget() returned !OK")
	}
	parsed, err := url.Parse(result.Target)
	if err != nil {
		t.Fatalf("url.Parse() = %v", err)
	}
	if parsed.Scheme != "postgres" || parsed.Host != "db.example:5432" || parsed.Path != "/app" {
		t.Fatalf("target = %q", result.Target)
	}
	if parsed.Query().Get("sslmode") != "require" {
		t.Fatalf("sslmode = %q, want require", parsed.Query().Get("sslmode"))
	}
}
