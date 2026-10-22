package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/lavr/express-botx/internal/config"
	vlog "github.com/lavr/express-botx/internal/log"
)

// Default cache lifetimes for chat membership. A positive answer is kept
// longer than a negative one: a bot just added to a chat should start
// receiving within seconds, while a bot removed from a chat losing access a
// minute late is the accepted cost of not calling BotX on every alert.
const (
	DefaultMembershipTTL         = config.DefaultMembershipTTL
	DefaultMembershipNegativeTTL = config.DefaultMembershipNegativeTTL

	// membershipLookupTimeout bounds one shared lookup. It runs detached from
	// any one request, so this context is the only thing that ends it; the
	// lookup must honour it (the BotX client does, through its HTTP request).
	membershipLookupTimeout = 30 * time.Second

	// membershipMaxEntries bounds the cache. Expired entries are swept when it
	// fills; if it is still full, it is cleared rather than left to grow on
	// requests for ever new chat IDs.
	membershipMaxEntries = 10000

	// membershipMaxInflight bounds concurrent lookups of distinct chats. A
	// request past it is answered like a failed lookup (503), not queued.
	membershipMaxInflight = 32
)

// ErrMembershipUnavailable is returned when a key scoped to the chats its bot
// is a member of could not have that membership checked: BotX was unreachable,
// timed out or answered with an error. The request is refused (fail closed),
// but with 503 rather than 403, so a sender that retries will retry. It is an
// ErrAccessUnavailable, which carries that status through a fan-out.
var ErrMembershipUnavailable = fmt.Errorf("chat membership could not be checked: %w", ErrAccessUnavailable)

var errMembershipBusy = errors.New("too many concurrent membership lookups")

// errNotMember is the refusal of a membership check: an ErrAccessDenied, and
// so an ErrChatNotAllowed, answered 403 even when it refuses a fan-out.
var errNotMember = fmt.Errorf("%w: the sending bot is not a member", ErrAccessDenied)

// MembershipLookup reports whether the bot that would send a message (by name;
// empty in single-bot mode) is currently a member of the chat with the given
// UUID. A chat that does not exist, or that the bot cannot see, is reported as
// not a member with a nil error. Any other failure is returned as an error and
// is never cached.
type MembershipLookup func(ctx context.Context, bot, chatID string) (bool, error)

// MembershipOptions configures the membership cache.
type MembershipOptions struct {
	TTL         time.Duration // positive answers; DefaultMembershipTTL when zero
	NegativeTTL time.Duration // negative answers; DefaultMembershipNegativeTTL when zero
}

// WithChatMembership enables API keys that are scoped to the chats their bot
// is a member of. Without it such a key cannot be authorized, and every request
// it makes is answered with 503.
func WithChatMembership(lookup MembershipLookup, opts MembershipOptions) Option {
	return func(s *Server) {
		s.membership = newMembershipChecker(lookup, opts)
	}
}

type membershipEntry struct {
	member  bool
	expires time.Time
}

type membershipCall struct {
	done   chan struct{}
	member bool
	err    error
}

// membershipChecker caches lookups per (bot, chat) and collapses concurrent
// lookups of the same pair into one call to BotX.
type membershipChecker struct {
	lookup      MembershipLookup
	ttl         time.Duration
	negativeTTL time.Duration
	now         func() time.Time

	mu       sync.Mutex
	cache    map[string]membershipEntry
	inflight map[string]*membershipCall
}

func newMembershipChecker(lookup MembershipLookup, opts MembershipOptions) *membershipChecker {
	m := &membershipChecker{
		lookup:      lookup,
		ttl:         opts.TTL,
		negativeTTL: opts.NegativeTTL,
		now:         time.Now,
		cache:       make(map[string]membershipEntry),
		inflight:    make(map[string]*membershipCall),
	}
	if m.ttl <= 0 {
		m.ttl = DefaultMembershipTTL
	}
	if m.negativeTTL <= 0 {
		m.negativeTTL = DefaultMembershipNegativeTTL
	}
	return m
}

// check reports whether bot is a member of chatID, and whether the answer came
// from the cache. A failed lookup returns an error and leaves the cache as it
// was.
func (m *membershipChecker) check(ctx context.Context, bot, chatID string) (member, cached bool, err error) {
	// Bot names are case-sensitive everywhere else, so two bots differing
	// only in case are two senders and must not share an answer. Chat UUIDs
	// are not, and are folded.
	key := bot + "\x00" + strings.ToLower(chatID)

	m.mu.Lock()
	now := m.now()
	if e, ok := m.cache[key]; ok {
		if now.Before(e.expires) {
			m.mu.Unlock()
			return e.member, true, nil
		}
		delete(m.cache, key)
	}
	call, ok := m.inflight[key]
	if !ok {
		if len(m.inflight) >= membershipMaxInflight {
			m.mu.Unlock()
			return false, false, errMembershipBusy
		}
		call = &membershipCall{done: make(chan struct{})}
		m.inflight[key] = call
		go m.run(key, call, bot, chatID)
	}
	m.mu.Unlock()

	// The lookup runs detached from any one request, so a caller that gives
	// up does not fail the others waiting on the same chat.
	select {
	case <-call.done:
		return call.member, false, call.err
	case <-ctx.Done():
		return false, false, ctx.Err()
	}
}

func (m *membershipChecker) run(key string, call *membershipCall, bot, chatID string) {
	ctx, cancel := context.WithTimeout(context.Background(), membershipLookupTimeout)
	defer cancel()
	call.member, call.err = m.lookup(ctx, bot, chatID)

	m.mu.Lock()
	delete(m.inflight, key)
	if call.err == nil {
		ttl := m.ttl
		if !call.member {
			ttl = m.negativeTTL
		}
		now := m.now()
		if len(m.cache) >= membershipMaxEntries {
			m.sweep(now)
		}
		m.cache[key] = membershipEntry{member: call.member, expires: now.Add(ttl)}
	}
	m.mu.Unlock()
	close(call.done)
}

// sweep drops expired entries and, if the cache is still full, all of them.
// Called with m.mu held.
func (m *membershipChecker) sweep(now time.Time) {
	for k, e := range m.cache {
		if !now.Before(e.expires) {
			delete(m.cache, k)
		}
	}
	if len(m.cache) >= membershipMaxEntries {
		clear(m.cache)
	}
}

// membershipGate re-checks membership right before a message to a resolved
// (bot, chat) is sent, for keys scoped by membership. authorizeTargets checks
// every target up front so the request gets one status, but delivery to the
// last of several chats can start well after the first was checked; this keeps
// each send within one cache lifetime of a check. Normally a cache hit.
func (s *Server) membershipGate(ctx context.Context, bot, chatID string) error {
	return s.checkMembership(ctx, chatID, bot, chatID)
}

// checkMembership checks one (bot, chat) pair and logs the outcome. It returns
// nil for a member, errNotMember (an ErrAccessDenied) for a non-member and an error wrapping
// ErrMembershipUnavailable when membership could not be checked. target is
// the chat as the request named it, for the log.
func (s *Server) checkMembership(ctx context.Context, target, bot, chatID string) error {
	if s.membership == nil {
		vlog.Info("scope: key %q is scoped by chat membership, but membership checks are not configured", KeyName(ctx))
		return ErrMembershipUnavailable
	}
	member, cached, err := s.membership.check(ctx, bot, chatID)
	source := "botx"
	if cached {
		source = "cache"
	}
	key, reqID := KeyName(ctx), middleware.GetReqID(ctx)
	switch {
	case err != nil:
		vlog.Info("scope: chat %q membership error [key: %s, request_id: %s, bot: %s, source: %s]: %s",
			target, key, reqID, botLabel(bot), source, boundedCause(err))
		return fmt.Errorf("%w: %v", ErrMembershipUnavailable, err)
	case !member:
		vlog.Info("scope: chat %q membership not_member [key: %s, request_id: %s, bot: %s, source: %s]",
			target, key, reqID, botLabel(bot), source)
		return errNotMember
	}
	vlog.Info("scope: chat %q membership member [key: %s, request_id: %s, bot: %s, source: %s]",
		target, key, reqID, botLabel(bot), source)
	return nil
}
