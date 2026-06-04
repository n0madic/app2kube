package cmd

import (
	"strings"
	"testing"

	"github.com/n0madic/app2kube/pkg/app2kube"
)

func TestRenderDotenv(t *testing.T) {
	app := app2kube.NewApp()
	app.ConfigMap = map[string]string{"B_CFG": "cfg"}
	app.Env = map[string]string{"A_ENV": "env"}
	app.Secrets = map[string]string{"C_SECRET": "sec"} // plaintext passes through

	out, err := renderDotenv(app, false, false)
	if err != nil {
		t.Fatalf("renderDotenv: %v", err)
	}
	want := "A_ENV=env\nB_CFG=cfg\nC_SECRET=sec\n"
	if out != want {
		t.Errorf("dotenv:\ngot  %q\nwant %q", out, want)
	}
}

// Regression (#12): a key present in more than one source (Env + Secrets) must
// be emitted once, not once per source, with the later source (secret) winning.
func TestRenderDotenvDeduplicatesKeys(t *testing.T) {
	app := app2kube.NewApp()
	app.Env = map[string]string{"DATABASE_URL": "from-env"}
	app.Secrets = map[string]string{"DATABASE_URL": "from-secret"} // same key

	out, err := renderDotenv(app, false, false)
	if err != nil {
		t.Fatalf("renderDotenv: %v", err)
	}
	if n := strings.Count(out, "DATABASE_URL="); n != 1 {
		t.Errorf("duplicate key must be emitted once, got %d lines:\n%s", n, out)
	}
	if !strings.Contains(out, "DATABASE_URL=from-secret") {
		t.Errorf("secret value must win over env, got:\n%s", out)
	}
}

func TestRenderDotenvExportQuotes(t *testing.T) {
	app := app2kube.NewApp()
	app.Env = map[string]string{"KEY": "value"}
	out, err := renderDotenv(app, true, true)
	if err != nil {
		t.Fatalf("renderDotenv: %v", err)
	}
	if out != "export KEY=\"value\"\n" {
		t.Errorf("export+quotes: got %q", out)
	}
}

func TestRenderDotenvDecryptError(t *testing.T) {
	app := app2kube.NewApp()
	app.Secrets = map[string]string{"k": "RSA#bogus"} // no key configured
	if _, err := renderDotenv(app, false, false); err == nil {
		t.Errorf("expected decryption error")
	}
}

func TestCollectDomains(t *testing.T) {
	app := app2kube.NewApp()
	app.Ingress = []app2kube.Ingress{
		{Host: "b.example.com", Aliases: []string{"a.example.com"}},
		{Host: "b.example.com"}, // duplicate must be removed
		{Host: "c.example.com"},
	}
	got := collectDomains(app)
	want := []string{"a.example.com", "b.example.com", "c.example.com"}
	if len(got) != len(want) {
		t.Fatalf("domains: got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("domain[%d]: got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestCollectDomainsEmpty(t *testing.T) {
	app := app2kube.NewApp()
	if got := collectDomains(app); got != nil {
		t.Errorf("expected nil for no ingress, got %v", got)
	}
}

// Regression (#6): under staging, GetIngress suppresses aliases (via
// IngressAliases), so `config domain` must not list them either or it reports
// domains the rendered Ingress does not serve.
func TestCollectDomainsSuppressesAliasesUnderStaging(t *testing.T) {
	app := app2kube.NewApp()
	app.Staging = "dev"
	app.Ingress = []app2kube.Ingress{
		{Host: "b.example.com", Aliases: []string{"a.example.com"}},
	}
	got := collectDomains(app)
	if len(got) != 1 || got[0] != "b.example.com" {
		t.Errorf("staging must suppress aliases, leaving only the host: got %v", got)
	}
}

func TestRenderSecrets(t *testing.T) {
	app := app2kube.NewApp()
	app.Secrets = map[string]string{"b": "two", "a": "one"}
	out, err := renderSecrets(app)
	if err != nil {
		t.Fatalf("renderSecrets: %v", err)
	}
	want := "secrets:\n  a: one\n  b: two\n"
	if out != want {
		t.Errorf("secrets:\ngot  %q\nwant %q", out, want)
	}
}
