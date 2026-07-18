// Package authdispatch reliably delivers direct OpenFGA relationship intents.
package authdispatch

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/vishu42/tflive/internal/authz"
)

const (
	defaultPollInterval  = time.Second
	defaultLeaseDuration = 30 * time.Second
	defaultRetryDelay    = 5 * time.Second
)

type Operation string

const (
	OperationGrant  Operation = "grant"
	OperationRevoke Operation = "revoke"
)

type Entry struct {
	ID        string
	Operation Operation
	Subject   string
	Stack     string
	Role      string
}

type Outbox interface {
	ClaimAuthorizationRelationship(context.Context, time.Time, time.Time) (Entry, bool, error)
	CompleteAuthorizationRelationship(context.Context, string) error
	RetryAuthorizationRelationship(context.Context, string, time.Time, string) error
	FailAuthorizationRelationship(context.Context, string, string) error
}

type Options struct {
	PollInterval  time.Duration
	LeaseDuration time.Duration
	RetryDelay    time.Duration
}

type Dispatcher struct {
	outbox        Outbox
	authorizer    authz.Authorizer
	pollInterval  time.Duration
	leaseDuration time.Duration
	retryDelay    time.Duration
}

func NewDispatcher(outbox Outbox, authorizer authz.Authorizer, options Options) *Dispatcher {
	if options.PollInterval <= 0 {
		options.PollInterval = defaultPollInterval
	}
	if options.LeaseDuration <= 0 {
		options.LeaseDuration = defaultLeaseDuration
	}
	if options.RetryDelay <= 0 {
		options.RetryDelay = defaultRetryDelay
	}
	return &Dispatcher{outbox: outbox, authorizer: authorizer, pollInterval: options.PollInterval, leaseDuration: options.LeaseDuration, retryDelay: options.RetryDelay}
}

func (dispatcher *Dispatcher) DispatchOnce(ctx context.Context, now time.Time) (bool, error) {
	entry, found, err := dispatcher.outbox.ClaimAuthorizationRelationship(ctx, now, now.Add(dispatcher.leaseDuration))
	if err != nil || !found {
		return found, err
	}

	grant, err := grantFromEntry(entry)
	if err != nil {
		if failErr := dispatcher.outbox.FailAuthorizationRelationship(ctx, entry.ID, "invalid authorization relationship intent"); failErr != nil {
			return true, fmt.Errorf("validate authorization relationship intent: %w (record failure: %v)", err, failErr)
		}
		return true, fmt.Errorf("validate authorization relationship intent: %w", err)
	}
	mutation, err := authz.NewMutation([]authz.Grant{grant}, true)
	if err != nil {
		return true, err
	}
	if entry.Operation == OperationGrant {
		err = dispatcher.authorizer.WriteRelationships(ctx, mutation)
	} else {
		err = dispatcher.authorizer.DeleteRelationships(ctx, mutation)
	}
	if err != nil {
		if retryErr := dispatcher.outbox.RetryAuthorizationRelationship(ctx, entry.ID, now.Add(dispatcher.retryDelay), errorSummary(err)); retryErr != nil {
			return true, fmt.Errorf("deliver authorization relationship: %w (record retry: %v)", err, retryErr)
		}
		return true, fmt.Errorf("deliver authorization relationship: %w", err)
	}
	if err := dispatcher.outbox.CompleteAuthorizationRelationship(ctx, entry.ID); err != nil {
		return true, fmt.Errorf("complete authorization relationship: %w", err)
	}
	return true, nil
}

func (dispatcher *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(dispatcher.pollInterval)
	defer ticker.Stop()
	for {
		dispatched, err := dispatcher.DispatchOnce(ctx, time.Now())
		if err != nil && ctx.Err() == nil {
			continue
		}
		if dispatched && err == nil {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func grantFromEntry(entry Entry) (authz.Grant, error) {
	if entry.Operation != OperationGrant && entry.Operation != OperationRevoke {
		return authz.Grant{}, fmt.Errorf("unknown operation %q", entry.Operation)
	}
	subject, err := authz.SubjectFromKeycloakSub(strings.TrimPrefix(entry.Subject, "user:"))
	if err != nil || subject.String() != entry.Subject {
		return authz.Grant{}, fmt.Errorf("invalid subject")
	}
	stack, err := authz.StackFromID(strings.TrimPrefix(entry.Stack, "stack:"))
	if err != nil || stack.String() != entry.Stack {
		return authz.Grant{}, fmt.Errorf("invalid stack")
	}
	role, err := authz.RoleFromDirectRelation(entry.Role)
	if err != nil {
		return authz.Grant{}, err
	}
	return authz.NewGrant(subject, stack, role)
}

func errorSummary(err error) string {
	switch {
	case errors.Is(err, authz.ErrTimeout):
		return "authorization timeout"
	case errors.Is(err, authz.ErrUnavailable):
		return "authorization unavailable"
	case errors.Is(err, authz.ErrWriteUnconfirmed):
		return "authorization write unconfirmed"
	default:
		return "authorization delivery failed"
	}
}
