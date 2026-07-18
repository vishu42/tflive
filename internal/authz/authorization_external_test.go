package authz_test

import (
	"reflect"
	"testing"

	"github.com/vishu42/tflive/internal/authz"
)

func TestRoleDoesNotExposeAForgeableValueField(t *testing.T) {
	roleType := reflect.TypeOf(authz.Role{})
	if roleType.NumField() != 1 || roleType.Field(0).PkgPath == "" {
		t.Fatal("Role must retain an unexported value field")
	}
}
