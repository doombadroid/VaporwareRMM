package ai

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
)

// Capability is the in-process descriptor for one AI feature. Capabilities
// register at init() time via Register; the database (ai_capabilities) is the
// per-tenant configuration surface. The descriptor is the schema-of-record;
// the DB row is the operator's settings.
type Capability struct {
	Name             string
	Category         Category
	Description      string
	Stage            int
	RequiredCaps     Capabilities       // what the chosen Provider must support
	DependsOn        []string           // other capability names OR system features ("device_classification")
	DefaultRung      Rung
	DefaultScope     ScopeFilter
	DefaultPromotion PromotionCriteria
	// PreferredTaskType drives routing. A summarisation capability won't fight
	// an operator who routes "reason" calls to an expensive model.
	PreferredTaskType TaskType
}

var (
	capsMu       sync.RWMutex
	capabilities = map[string]Capability{}
)

// Register adds a capability to the in-process registry. Stage 1+ files call
// this from init(). Re-registering a name panics — that's a programming bug.
func Register(c Capability) {
	if c.Name == "" {
		panic("ai: capability missing Name")
	}
	if c.DefaultRung == "" {
		c.DefaultRung = RungShadow
	}
	capsMu.Lock()
	defer capsMu.Unlock()
	if _, exists := capabilities[c.Name]; exists {
		panic("ai: capability already registered: " + c.Name)
	}
	capabilities[c.Name] = c
}

// Lookup returns a capability by name. Returns false if unknown — handlers
// must distinguish "unknown capability" (programming error) from "capability
// disabled for this tenant" (operator config).
func Lookup(name string) (Capability, bool) {
	capsMu.RLock()
	defer capsMu.RUnlock()
	c, ok := capabilities[name]
	return c, ok
}

// All returns every registered capability sorted by name. Used by the
// dashboard's capability table.
func All() []Capability {
	capsMu.RLock()
	defer capsMu.RUnlock()
	out := make([]Capability, 0, len(capabilities))
	for _, c := range capabilities {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SystemFeatures is the registry of non-capability dependencies (e.g.
// "device_classification") that capability deps can resolve to. Stage 0 ships
// an empty set; Stage 1 registers things like device_classification before
// capabilities that depend on it become enableable.
var (
	sysMu       sync.RWMutex
	sysFeatures = map[string]func() error{}
)

// RegisterSystemFeature attaches a name to a readiness probe. Probe returns
// nil when the system feature is ready to be relied on.
func RegisterSystemFeature(name string, ready func() error) {
	sysMu.Lock()
	defer sysMu.Unlock()
	sysFeatures[name] = ready
}

// CheckDependencies validates every dependency declared by a capability. A
// missing dependency is an error and the capability cannot be enabled. We
// return the list of unmet deps so the dashboard can show a useful message.
func CheckDependencies(name string) (unmet []string, err error) {
	c, ok := Lookup(name)
	if !ok {
		return nil, fmt.Errorf("ai: unknown capability %q", name)
	}
	for _, dep := range c.DependsOn {
		// Capability dep
		if _, ok := Lookup(dep); ok {
			continue
		}
		// System feature dep
		sysMu.RLock()
		probe, ok := sysFeatures[dep]
		sysMu.RUnlock()
		if !ok {
			unmet = append(unmet, dep)
			continue
		}
		if err := probe(); err != nil {
			unmet = append(unmet, dep+" ("+err.Error()+")")
		}
	}
	return unmet, nil
}

// ScopeFilterJSON is a small helper so handlers can persist/parse scope
// filters consistently.
func ScopeFilterJSON(s ScopeFilter) ([]byte, error)            { return json.Marshal(s) }
func ParseScopeFilter(raw []byte) (ScopeFilter, error)         {
	var s ScopeFilter
	if len(raw) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, err
	}
	return s, nil
}
