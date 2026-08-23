package authz_test

import (
	"reflect"
	"testing"

	"github.com/vishu42/tflive/internal/authz"
)

func TestRelationDoesNotExposeAForgeableValueField(t *testing.T) {
	relationType := reflect.TypeOf(authz.Relation{})
	if relationType.NumField() != 1 || relationType.Field(0).PkgPath == "" {
		t.Fatal("Relation must retain an unexported value field")
	}
}
