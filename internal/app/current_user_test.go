package app

import (
	"context"
	"testing"

	"github.com/vishu42/tflive/internal/authn"
)

func TestGetMeReturnsIdentityAndCapabilities(t *testing.T) {
	t.Parallel()

	service := NewService(Service{})
	ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{
		Subject:           "user-123",
		Name:              "Alice Smith",
		PreferredUsername: "alice",
		Email:             "alice@example.com",
		RealmRoles:        []string{"platform-admin", "stack-creator"},
	})

	me, err := service.GetMe(ctx)
	if err != nil {
		t.Fatalf("GetMe() error = %v", err)
	}
	if me.Subject != "user-123" {
		t.Fatalf("subject = %q, want user-123", me.Subject)
	}
	if me.Name != "Alice Smith" {
		t.Fatalf("name = %q, want Alice Smith", me.Name)
	}
	if me.PreferredUsername != "alice" {
		t.Fatalf("preferred_username = %q, want alice", me.PreferredUsername)
	}
	if me.Email != "alice@example.com" {
		t.Fatalf("email = %q, want alice@example.com", me.Email)
	}
	if !me.GlobalCapabilities.CanCreateStacks {
		t.Fatal("can_create_stacks = false, want true")
	}
	if !me.GlobalCapabilities.IsPlatformAdmin {
		t.Fatal("is_platform_admin = false, want true")
	}
}

func TestGetMeRequiresAuthentication(t *testing.T) {
	t.Parallel()

	service := NewService(Service{})

	_, err := service.GetMe(context.Background())
	if err != ErrUnauthenticated {
		t.Fatalf("GetMe() error = %v, want ErrUnauthenticated", err)
	}
}

func TestGetMeDerivesCapabilitiesFromRoles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		roles           []string
		canCreateStacks bool
		isPlatformAdmin bool
	}{
		{name: "stack-creator only", roles: []string{"stack-creator"}, canCreateStacks: true, isPlatformAdmin: false},
		{name: "platform-admin only", roles: []string{"platform-admin"}, canCreateStacks: true, isPlatformAdmin: true},
		{name: "no roles", roles: nil, canCreateStacks: false, isPlatformAdmin: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			service := NewService(Service{})
			ctx := authn.ContextWithPrincipal(context.Background(), authn.Principal{
				Subject:    "user-123",
				RealmRoles: tt.roles,
			})

			me, err := service.GetMe(ctx)
			if err != nil {
				t.Fatalf("GetMe() error = %v", err)
			}
			if me.GlobalCapabilities.CanCreateStacks != tt.canCreateStacks {
				t.Fatalf("can_create_stacks = %v, want %v", me.GlobalCapabilities.CanCreateStacks, tt.canCreateStacks)
			}
			if me.GlobalCapabilities.IsPlatformAdmin != tt.isPlatformAdmin {
				t.Fatalf("is_platform_admin = %v, want %v", me.GlobalCapabilities.IsPlatformAdmin, tt.isPlatformAdmin)
			}
		})
	}
}
