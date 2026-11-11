package tfdriftcheck

import (
	"errors"
	"testing"
)

func TestValidateRule(t *testing.T) {
	valid := []string{"aws_instance.*:tags.Env", "r:*", "*.web:tags.Name"}
	for _, rule := range valid {
		if err := validateRule(rule); err != nil {
			t.Errorf("validateRule(%q) = %v, want nil", rule, err)
		}
	}
	invalid := []string{"no-colon", ":tags.Env", "aws_instance.*:", "aws_instance.*:tags["}
	for _, rule := range invalid {
		err := validateRule(rule)
		var e *Error
		if !errors.As(err, &e) || e.Code != "invalid_rule" {
			t.Errorf("validateRule(%q) = %v, want Code invalid_rule", rule, err)
		}
	}
}
