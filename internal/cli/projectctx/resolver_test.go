package projectctx

import "testing"

func TestExplicitResolverReturnsProjectName(t *testing.T) {
	resolver := ExplicitResolver{CommandPath: "ovek status"}

	projectName, err := resolver.Resolve([]string{"demo-app"})
	if err != nil {
		t.Fatalf("expected resolve to succeed, got error: %v", err)
	}
	if projectName != "demo-app" {
		t.Fatalf("expected project name %q, got %q", "demo-app", projectName)
	}
}

func TestExplicitResolverRejectsWrongArgCount(t *testing.T) {
	resolver := ExplicitResolver{CommandPath: "ovek status"}

	if _, err := resolver.Resolve(nil); err == nil {
		t.Fatal("expected resolver to reject missing project argument")
	}
}
