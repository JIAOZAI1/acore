package agentstrategy

import (
	"context"
	"fmt"

	"github.com/JIAOZAI1/acore/internal/nilcheck"
	"github.com/JIAOZAI1/acore/model"
	"github.com/JIAOZAI1/acore/session"
)

type SessionRunState struct {
	service  session.Service
	key      session.Key
	revision session.Revision
	input    []model.Message
}

func validateRequestInput(req Request) error {
	hasMessages := len(req.Messages) > 0
	hasSession := req.Session != nil
	if hasMessages == hasSession {
		return fmt.Errorf("%w: exactly one of messages and session must be set", ErrInvalidRequest)
	}
	if !hasSession {
		return nil
	}
	if req.Session.Key.Scope == "" {
		return fmt.Errorf("%w: %w: empty session scope", ErrInvalidRequest, session.ErrInvalidKey)
	}
	if req.Session.Key.ID == "" {
		return fmt.Errorf("%w: %w: empty session ID", ErrInvalidRequest, session.ErrInvalidKey)
	}
	if len(req.Session.Messages) == 0 {
		return fmt.Errorf("%w: session messages must not be empty", ErrInvalidRequest)
	}
	return nil
}

func prepareSessionRun(ctx context.Context, service session.Service, req Request) (Request, *SessionRunState, error) {
	if err := validateRequestInput(req); err != nil {
		return Request{}, nil, err
	}
	prepared := Request{Messages: cloneMessages(req.Messages), Options: cloneModelOptions(req.Options)}
	if req.Session == nil {
		return prepared, nil, nil
	}
	if nilcheck.IsNil(service) {
		return Request{}, nil, ErrSessionUnsupported
	}

	snapshot, err := service.Load(ctx, req.Session.Key)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Request{}, nil, ctxErr
	}
	if err != nil {
		return Request{}, nil, fmt.Errorf("%w: %w", ErrLoadSession, err)
	}
	if (snapshot.Revision == 0) != (len(snapshot.Messages) == 0) {
		return Request{}, nil, fmt.Errorf("%w: %w: inconsistent revision and messages", ErrLoadSession, session.ErrInvalidSnapshot)
	}

	input := cloneMessages(req.Session.Messages)
	prepared.Messages = make([]model.Message, 0, len(snapshot.Messages)+len(input))
	prepared.Messages = append(prepared.Messages, cloneMessages(snapshot.Messages)...)
	prepared.Messages = append(prepared.Messages, cloneMessages(input)...)
	state := &SessionRunState{
		service:  service,
		key:      req.Session.Key,
		revision: snapshot.Revision,
		input:    input,
	}
	return prepared, state, nil
}

func commitSessionRun(ctx context.Context, state *SessionRunState, result *Result) error {
	if state == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	messages := make([]model.Message, 0, len(state.input)+len(result.GeneratedMessages))
	messages = append(messages, cloneMessages(state.input)...)
	messages = append(messages, cloneMessages(result.GeneratedMessages)...)
	_, err := state.service.Append(ctx, state.key, state.revision, messages)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	if err != nil {
		return fmt.Errorf("%w: %w", ErrCommitSession, err)
	}
	return nil
}
