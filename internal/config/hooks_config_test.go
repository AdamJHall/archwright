package config

import (
	"strings"
	"testing"
)

// validateHook runs the validator over a single Hook (via the same custom
// rules the full Config uses) and returns the resulting error, if any.
func validateHook(h Hook) error {
	return validate.Struct(h)
}

func TestHook_ValidGlobalPoint(t *testing.T) {
	if err := validateHook(Hook{Name: "h", At: "post-install", Run: "echo hi"}); err != nil {
		t.Errorf("valid hook should pass: %v", err)
	}
}

func TestHook_ValidPerStagePoint(t *testing.T) {
	if err := validateHook(Hook{Name: "h", At: "before:packages", Run: "echo hi"}); err != nil {
		t.Errorf("before:packages is well-formed and should pass hookpoint: %v", err)
	}
}

func TestHook_BadAtFailsHookpoint(t *testing.T) {
	err := validateHook(Hook{Name: "h", At: "midway", Run: "echo hi"})
	if err == nil {
		t.Fatal("bad at should fail hookpoint")
	}
	if !strings.Contains(err.Error(), "hookpoint") {
		t.Errorf("expected hookpoint failure, got: %v", err)
	}
}

func TestHook_EmptyStageTokenFailsHookpoint(t *testing.T) {
	err := validateHook(Hook{Name: "h", At: "before:", Run: "echo hi"})
	if err == nil {
		t.Fatal("before: with empty stage should fail hookpoint")
	}
	if !strings.Contains(err.Error(), "hookpoint") {
		t.Errorf("expected hookpoint failure, got: %v", err)
	}
}

func TestHook_NeitherRunNorScriptFails(t *testing.T) {
	err := validateHook(Hook{Name: "h", At: "post-install"})
	if err == nil {
		t.Fatal("hook with neither run nor script should fail required_without")
	}
	if !strings.Contains(err.Error(), "required_without") {
		t.Errorf("expected required_without failure, got: %v", err)
	}
}
