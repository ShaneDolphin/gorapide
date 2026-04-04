package pattern

import (
	"fmt"
	"strings"

	"github.com/beautiful-majestic-dolphin/gorapide"
)

// mergeEventSets returns the union of two EventSets, deduplicating by EventID.
func mergeEventSets(a, b gorapide.EventSet) gorapide.EventSet {
	seen := make(map[gorapide.EventID]bool, len(a)+len(b))
	result := make(gorapide.EventSet, 0, len(a)+len(b))
	for _, e := range a {
		if !seen[e.ID] {
			seen[e.ID] = true
			result = append(result, e)
		}
	}
	for _, e := range b {
		if !seen[e.ID] {
			seen[e.ID] = true
			result = append(result, e)
		}
	}
	return result
}

// eventIDSet builds a lookup set from an EventSet.
func eventIDSet(es gorapide.EventSet) map[gorapide.EventID]bool {
	m := make(map[gorapide.EventID]bool, len(es))
	for _, e := range es {
		m[e.ID] = true
	}
	return m
}

// --- Sequence Pattern (P1 -> P2) ---

// seqPattern matches when every event in a P1 match causally precedes
// every event in a P2 match.
type seqPattern struct {
	subs []Pattern
}

// Seq creates a sequence pattern. For 2 patterns, it matches when every
// event in P1's match causally precedes every event in P2's match.
// For 3+ patterns, Seq(A, B, C) is equivalent to Seq(Seq(A, B), C).
func Seq(patterns ...Pattern) Pattern {
	if len(patterns) < 2 {
		panic("pattern.Seq: requires at least 2 sub-patterns")
	}
	if len(patterns) == 2 {
		return &seqPattern{subs: patterns}
	}
	// Chain: Seq(A, B, C) = Seq(Seq(A, B), C)
	left := Seq(patterns[:len(patterns)-1]...)
	return &seqPattern{subs: []Pattern{left, patterns[len(patterns)-1]}}
}

func (sp *seqPattern) Match(p PosetReader) []gorapide.EventSet {
	leftMatches := sp.subs[0].Match(p)
	rightMatches := sp.subs[1].Match(p)
	results := make([]gorapide.EventSet, 0)

	for _, s1 := range leftMatches {
		for _, s2 := range rightMatches {
			if allCausallyBefore(p, s1, s2) {
				results = append(results, mergeEventSets(s1, s2))
			}
		}
	}
	return results
}

func (sp *seqPattern) String() string {
	parts := make([]string, len(sp.subs))
	for i, s := range sp.subs {
		parts[i] = s.String()
	}
	return fmt.Sprintf("Seq(%s)", strings.Join(parts, ", "))
}

// allCausallyBefore returns true if every event in 'before' causally
// precedes every event in 'after'.
func allCausallyBefore(p PosetReader, before, after gorapide.EventSet) bool {
	for _, e1 := range before {
		for _, e2 := range after {
			if !p.IsCausallyBefore(e1.ID, e2.ID) {
				return false
			}
		}
	}
	return true
}

// --- Immediate Sequence Pattern (P1 ~> P2) ---

// immSeqPattern matches like Seq but additionally requires no intervening
// events between the P1 and P2 match sets in the causal chain.
type immSeqPattern struct {
	p1, p2 Pattern
}

// ImmSeq creates an immediate sequence pattern. Matches when P1 and P2
// match, every event in P1 causally precedes every event in P2, and there
// are no intervening events w where e1 <c w <c e2 that are not in either
// match set.
func ImmSeq(p1, p2 Pattern) Pattern {
	if p1 == nil || p2 == nil {
		panic("pattern.ImmSeq: requires two non-nil sub-patterns")
	}
	return &immSeqPattern{p1: p1, p2: p2}
}

func (ip *immSeqPattern) Match(p PosetReader) []gorapide.EventSet {
	leftMatches := ip.p1.Match(p)
	rightMatches := ip.p2.Match(p)
	allEvents := p.All()
	results := make([]gorapide.EventSet, 0)

	for _, s1 := range leftMatches {
		for _, s2 := range rightMatches {
			if !allCausallyBefore(p, s1, s2) {
				continue
			}
			if hasIntervening(p, s1, s2, allEvents) {
				continue
			}
			results = append(results, mergeEventSets(s1, s2))
		}
	}
	return results
}

// hasIntervening checks whether any event w (not in s1 or s2) exists such
// that some e1 in s1 <c w and w <c some e2 in s2.
func hasIntervening(p PosetReader, s1, s2, all gorapide.EventSet) bool {
	s1IDs := eventIDSet(s1)
	s2IDs := eventIDSet(s2)
	for _, w := range all {
		if s1IDs[w.ID] || s2IDs[w.ID] {
			continue
		}
		// Check if w sits between any e1 and e2.
		for _, e1 := range s1 {
			if !p.IsCausallyBefore(e1.ID, w.ID) {
				continue
			}
			for _, e2 := range s2 {
				if p.IsCausallyBefore(w.ID, e2.ID) {
					return true
				}
			}
		}
	}
	return false
}

func (ip *immSeqPattern) String() string {
	return fmt.Sprintf("ImmSeq(%s, %s)", ip.p1.String(), ip.p2.String())
}

// --- Join Pattern (P1 && P2) ---

// joinPattern matches when P1 and P2 both match and their match sets share
// at least one common causal ancestor.
type joinPattern struct {
	p1, p2 Pattern
}

// Join creates a join pattern. Matches when P1 and P2 both match and
// there exists at least one common causal ancestor of events in both
// match sets (they converge from a shared cause).
func Join(p1, p2 Pattern) Pattern {
	if p1 == nil || p2 == nil {
		panic("pattern.Join: requires two non-nil sub-patterns")
	}
	return &joinPattern{p1: p1, p2: p2}
}

func (jp *joinPattern) Match(p PosetReader) []gorapide.EventSet {
	leftMatches := jp.p1.Match(p)
	rightMatches := jp.p2.Match(p)
	results := make([]gorapide.EventSet, 0)

	for _, s1 := range leftMatches {
		for _, s2 := range rightMatches {
			if sharesCausalAncestor(p, s1, s2) {
				results = append(results, mergeEventSets(s1, s2))
			}
		}
	}
	return results
}

// sharesCausalAncestor returns true if there exists at least one event that
// is a causal ancestor of some event in s1 AND of some event in s2.
// An event being directly in both sets also counts (it is its own ancestor
// in the trivial sense), but since we want a shared cause, we check
// actual ancestors.
func sharesCausalAncestor(p PosetReader, s1, s2 gorapide.EventSet) bool {
	// Collect all ancestors of s1 events.
	s1Ancestors := make(map[gorapide.EventID]bool)
	for _, e := range s1 {
		// The event itself could be an ancestor of s2 events.
		s1Ancestors[e.ID] = true
		for _, anc := range p.CausalAncestors(e.ID) {
			s1Ancestors[anc.ID] = true
		}
	}
	// Check if any ancestor of s2 events is in s1's ancestor set.
	for _, e := range s2 {
		if s1Ancestors[e.ID] {
			return true
		}
		for _, anc := range p.CausalAncestors(e.ID) {
			if s1Ancestors[anc.ID] {
				return true
			}
		}
	}
	return false
}

func (jp *joinPattern) String() string {
	return fmt.Sprintf("Join(%s, %s)", jp.p1.String(), jp.p2.String())
}

// --- Independence Pattern (P1 || P2) ---

// independentPattern matches when P1 and P2 both match and no event in
// either match set causally relates to any event in the other.
type independentPattern struct {
	p1, p2 Pattern
}

// Independent creates an independence pattern. Matches when P1 and P2
// both match and all events across the two match sets are pairwise
// causally independent.
func Independent(p1, p2 Pattern) Pattern {
	if p1 == nil || p2 == nil {
		panic("pattern.Independent: requires two non-nil sub-patterns")
	}
	return &independentPattern{p1: p1, p2: p2}
}

func (ip *independentPattern) Match(p PosetReader) []gorapide.EventSet {
	leftMatches := ip.p1.Match(p)
	rightMatches := ip.p2.Match(p)
	results := make([]gorapide.EventSet, 0)

	for _, s1 := range leftMatches {
		for _, s2 := range rightMatches {
			if allIndependent(p, s1, s2) {
				results = append(results, mergeEventSets(s1, s2))
			}
		}
	}
	return results
}

// allIndependent returns true if every pair (e1, e2) across the two sets
// is causally independent.
func allIndependent(p PosetReader, s1, s2 gorapide.EventSet) bool {
	for _, e1 := range s1 {
		for _, e2 := range s2 {
			if !p.IsCausallyIndependent(e1.ID, e2.ID) {
				return false
			}
		}
	}
	return true
}

func (ip *independentPattern) String() string {
	return fmt.Sprintf("Independent(%s, %s)", ip.p1.String(), ip.p2.String())
}

// --- Disjunction Pattern (P1 or P2) ---

// orPattern returns the union of match sets from its sub-patterns.
// A match satisfies any one of the sub-patterns.
type orPattern struct {
	subs []Pattern
}

// Or creates a disjunction pattern. Returns the union of all match sets
// from the sub-patterns, deduplicated by event set identity.
func Or(patterns ...Pattern) Pattern {
	if len(patterns) < 2 {
		panic("pattern.Or: requires at least 2 sub-patterns")
	}
	return &orPattern{subs: patterns}
}

// idKey is a canonical string key for deduplicating EventSets.
type idKey string

func (op *orPattern) Match(p PosetReader) []gorapide.EventSet {
	// Collect all matches, dedup by the set of event IDs in each match.
	seen := make(map[idKey]bool)
	results := make([]gorapide.EventSet, 0)

	for _, sub := range op.subs {
		for _, es := range sub.Match(p) {
			key := eventSetKey(es)
			if !seen[key] {
				seen[key] = true
				results = append(results, es)
			}
		}
	}
	return results
}

// eventSetKey produces a canonical string key for an EventSet for dedup.
func eventSetKey(es gorapide.EventSet) idKey {
	ids := make([]string, len(es))
	for i, e := range es {
		ids[i] = string(e.ID)
	}
	// Sort for canonical ordering.
	sortStrings(ids)
	return idKey(strings.Join(ids, "|"))
}

// sortStrings sorts a slice of strings in place (avoids importing sort
// just for this).
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func (op *orPattern) String() string {
	parts := make([]string, len(op.subs))
	for i, s := range op.subs {
		parts[i] = s.String()
	}
	return fmt.Sprintf("Or(%s)", strings.Join(parts, ", "))
}

// --- Conjunction Pattern (P1 and P2) ---

// andPattern returns only match sets that appear in ALL sub-patterns.
type andPattern struct {
	subs []Pattern
}

// And creates a conjunction pattern. Returns only event sets that match
// ALL of the sub-patterns. Match identity is based on the set of EventIDs.
func And(patterns ...Pattern) Pattern {
	if len(patterns) < 2 {
		panic("pattern.And: requires at least 2 sub-patterns")
	}
	return &andPattern{subs: patterns}
}

func (ap *andPattern) Match(p PosetReader) []gorapide.EventSet {
	if len(ap.subs) == 0 {
		return nil
	}
	// Start with first sub-pattern's matches.
	current := ap.subs[0].Match(p)
	if len(current) == 0 {
		return make([]gorapide.EventSet, 0)
	}

	for _, sub := range ap.subs[1:] {
		nextMatches := sub.Match(p)
		nextKeys := make(map[idKey]bool, len(nextMatches))
		for _, es := range nextMatches {
			nextKeys[eventSetKey(es)] = true
		}
		filtered := make([]gorapide.EventSet, 0)
		for _, es := range current {
			if nextKeys[eventSetKey(es)] {
				filtered = append(filtered, es)
			}
		}
		current = filtered
		if len(current) == 0 {
			return make([]gorapide.EventSet, 0)
		}
	}
	return current
}

func (ap *andPattern) String() string {
	parts := make([]string, len(ap.subs))
	for i, s := range ap.subs {
		parts[i] = s.String()
	}
	return fmt.Sprintf("And(%s)", strings.Join(parts, ", "))
}

// --- Union Pattern (P1 U P2) ---

// unionPattern combines events from P1 matches with P2 matches into
// larger sets. Every s1 from P1 and s2 from P2 produces s1+s2.
type unionPattern struct {
	p1, p2 Pattern
}

// Union creates a union pattern. For every match s1 from P1 and s2 from P2,
// produces the merged set s1+s2.
func Union(p1, p2 Pattern) Pattern {
	if p1 == nil || p2 == nil {
		panic("pattern.Union: requires two non-nil sub-patterns")
	}
	return &unionPattern{p1: p1, p2: p2}
}

func (up *unionPattern) Match(p PosetReader) []gorapide.EventSet {
	leftMatches := up.p1.Match(p)
	rightMatches := up.p2.Match(p)
	results := make([]gorapide.EventSet, 0, len(leftMatches)*len(rightMatches))

	for _, s1 := range leftMatches {
		for _, s2 := range rightMatches {
			results = append(results, mergeEventSets(s1, s2))
		}
	}
	return results
}

func (up *unionPattern) String() string {
	return fmt.Sprintf("Union(%s, %s)", up.p1.String(), up.p2.String())
}

// --- Iteration Pattern (for x in set op P) ---

// forEachPattern matches pattern P for each element of a collection,
// combining results with a specified operator.
type forEachPattern struct {
	inner Pattern
	label string
}

// ForEach creates an iteration pattern. For each item in items, fn produces
// a pattern. The patterns are combined pairwise using op (e.g., Seq, Join).
// If items has 0 elements, returns a pattern that matches nothing.
// If items has 1 element, returns fn(items[0]) directly.
func ForEach[T any](items []T, op func(Pattern, Pattern) Pattern, fn func(T) Pattern) Pattern {
	if len(items) == 0 {
		return &forEachPattern{
			inner: &emptyPattern{},
			label: "ForEach([])",
		}
	}
	if len(items) == 1 {
		inner := fn(items[0])
		return &forEachPattern{
			inner: inner,
			label: fmt.Sprintf("ForEach([1], %s)", inner.String()),
		}
	}
	// Combine pairwise: op(fn(items[0]), op(fn(items[1]), ...))
	// Left-fold: result = op(result, fn(items[i]))
	result := fn(items[0])
	for _, item := range items[1:] {
		result = op(result, fn(item))
	}
	return &forEachPattern{
		inner: result,
		label: fmt.Sprintf("ForEach([%d])", len(items)),
	}
}

func (fp *forEachPattern) Match(p PosetReader) []gorapide.EventSet {
	return fp.inner.Match(p)
}

func (fp *forEachPattern) String() string {
	return fp.label
}

// emptyPattern always returns no matches.
type emptyPattern struct{}

func (ep *emptyPattern) Match(_ PosetReader) []gorapide.EventSet {
	return make([]gorapide.EventSet, 0)
}

func (ep *emptyPattern) String() string {
	return "Empty()"
}

// --- Guard Pattern (P where B) ---

// guardPattern only evaluates p if condition() returns true.
type guardPattern struct {
	p         Pattern
	condition func() bool
}

// Guard creates a guarded pattern. The inner pattern p is only evaluated
// if condition() returns true. Otherwise, returns no matches.
func Guard(p Pattern, condition func() bool) Pattern {
	if p == nil {
		panic("pattern.Guard: requires a non-nil sub-pattern")
	}
	return &guardPattern{p: p, condition: condition}
}

func (gp *guardPattern) Match(pr PosetReader) []gorapide.EventSet {
	if !gp.condition() {
		return make([]gorapide.EventSet, 0)
	}
	return gp.p.Match(pr)
}

func (gp *guardPattern) String() string {
	return fmt.Sprintf("Guard(%s)", gp.p.String())
}

// --- Negation Pattern ---

// notPattern is a marker pattern indicating "this pattern must NOT match."
// It does not produce match sets itself; it is used by constraint checkers
// to express negative conditions.
type notPattern struct {
	p Pattern
}

// Not creates a negation pattern. This is a marker: when matched, it always
// returns an empty result set. Its purpose is to be recognized by constraint
// checkers as indicating the wrapped pattern must NOT match.
func Not(p Pattern) Pattern {
	if p == nil {
		panic("pattern.Not: requires a non-nil sub-pattern")
	}
	return &notPattern{p: p}
}

// IsNot reports whether p is a negation pattern. If so, returns the
// negated inner pattern.
func IsNot(p Pattern) (Pattern, bool) {
	np, ok := p.(*notPattern)
	if !ok {
		return nil, false
	}
	return np.p, true
}

func (np *notPattern) Match(_ PosetReader) []gorapide.EventSet {
	return make([]gorapide.EventSet, 0)
}

func (np *notPattern) String() string {
	return fmt.Sprintf("Not(%s)", np.p.String())
}
