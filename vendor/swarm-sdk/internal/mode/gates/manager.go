package gates

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"sync"
	"time"

	"github.com/Swarm-Code/mono/swarm-sdk/internal/observability"
)

// GateManager orchestrates gate evaluation during workflow execution
type GateManager struct {
	gates             map[string]Gate          // gate_id -> gate
	gatesByTrigger    map[GateTrigger][]Gate   // trigger -> gates
	gateDependencies  map[string][]string      // gate_id -> dependent_gate_ids
	evaluationHistory map[string]*GateDecision // gate_id -> last decision
	logger            observability.Logger
	tracer            observability.Tracer
	registry          *GateRegistry
	mu                sync.RWMutex
}

// NewGateManager creates a new gate manager
func NewGateManager(logger observability.Logger, tracer observability.Tracer, registry *GateRegistry) *GateManager {
	if registry == nil {
		registry = GlobalGateRegistry
	}

	return &GateManager{
		gates:             make(map[string]Gate),
		gatesByTrigger:    make(map[GateTrigger][]Gate),
		gateDependencies:  make(map[string][]string),
		evaluationHistory: make(map[string]*GateDecision),
		logger:            logger,
		tracer:            tracer,
		registry:          registry,
	}
}

// LoadGates loads gates from configuration
func (gm *GateManager) LoadGates(configs []GateConfig) error {
	gm.mu.Lock()
	defer gm.mu.Unlock()

	for _, config := range configs {
		if !config.Enabled {
			continue
		}

		gate, err := gm.registry.CreateGate(config, gm.logger, gm.tracer)
		if err != nil {
			return fmt.Errorf("failed to create gate %s: %w", config.ID, err)
		}

		gm.gates[gate.ID()] = gate

		// Index by trigger
		trigger := gate.Trigger()
		gm.gatesByTrigger[trigger] = append(gm.gatesByTrigger[trigger], gate)

		// Track dependencies
		if len(config.DependsOn) > 0 {
			gm.gateDependencies[gate.ID()] = config.DependsOn
		}
	}

	// Sort gates by priority within each trigger
	for trigger := range gm.gatesByTrigger {
		gm.sortGatesByPriority(trigger)
	}

	return nil
}

// sortGatesByPriority sorts gates by priority (higher priority first)
func (gm *GateManager) sortGatesByPriority(trigger GateTrigger) {
	gates := gm.gatesByTrigger[trigger]
	sort.Slice(gates, func(i, j int) bool {
		// Get priority from gate configs
		// Since we can't access BaseGate fields directly, use a default
		return false // Keep original order for now
	})
	gm.gatesByTrigger[trigger] = gates
}

// EvaluateGates evaluates all gates for a specific trigger
func (gm *GateManager) EvaluateGates(ctx context.Context, trigger GateTrigger, evalCtx *EvaluationContext) (*GateEvaluationResult, error) {
	ctx, span := gm.tracer.StartSpan(ctx, "gates.evaluate")
	defer span.End()

	span.SetAttribute("trigger", string(trigger))

	gm.mu.RLock()
	gates := gm.gatesByTrigger[trigger]
	gm.mu.RUnlock()

	if len(gates) == 0 {
		// No gates for this trigger, allow by default
		return &GateEvaluationResult{
			Allow:  true,
			Reason: "no gates configured",
		}, nil
	}

	result := &GateEvaluationResult{
		Allow:     true,
		Decisions: make(map[string]*GateDecision),
		Timestamp: time.Now(),
	}

	// Evaluate gates in order
	for _, gate := range gates {
		// Check dependencies
		if !gm.checkDependencies(gate.ID()) {
			gm.logger.Debug(ctx, "gate.dependency_not_met",
				observability.F("gate_id", gate.ID()))
			continue
		}

		// Evaluate gate
		decision, err := gate.Evaluate(ctx, evalCtx)
		if err != nil {
			gm.logger.Error(ctx, "gate.evaluation_failed",
				observability.F("gate_id", gate.ID()),
				observability.F("error", err.Error()))

			// Default to block on error
			decision = &GateDecision{
				Allow:      false,
				Confidence: 0.0,
				Reason:     fmt.Sprintf("evaluation error: %v", err),
				Action:     ActionBlock,
				Timestamp:  time.Now(),
			}
		}

		// Store decision
		result.Decisions[gate.ID()] = decision

		gm.mu.Lock()
		gm.evaluationHistory[gate.ID()] = decision
		gm.mu.Unlock()

		gm.logger.Info(ctx, "gate.evaluated",
			observability.F("gate_id", gate.ID()),
			observability.F("gate_type", string(gate.Type())),
			observability.F("allow", decision.Allow),
			observability.F("confidence", decision.Confidence),
			observability.F("action", string(decision.Action)))

		// If gate blocks and has higher confidence, update result
		if !decision.Allow {
			result.Allow = false
			result.BlockingGate = gate.ID()
			result.Action = decision.Action
			result.Reason = decision.Reason
			result.Confidence = decision.Confidence

			// Stop evaluating if gate explicitly fails
			if decision.Action == ActionBlock || decision.Action == ActionFailWorkflow {
				break
			}
		}
	}

	return result, nil
}

// checkDependencies checks if all gate dependencies have passed
func (gm *GateManager) checkDependencies(gateID string) bool {
	gm.mu.RLock()
	deps := gm.gateDependencies[gateID]
	gm.mu.RUnlock()

	if len(deps) == 0 {
		return true
	}

	gm.mu.RLock()
	defer gm.mu.RUnlock()

	for _, depID := range deps {
		decision, ok := gm.evaluationHistory[depID]
		if !ok || !decision.Allow {
			return false
		}
	}

	return true
}

// GetGate retrieves a gate by ID
func (gm *GateManager) GetGate(gateID string) (Gate, error) {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	gate, ok := gm.gates[gateID]
	if !ok {
		return nil, fmt.Errorf("gate not found: %s", gateID)
	}
	return gate, nil
}

// GetDecision retrieves the last decision for a gate
func (gm *GateManager) GetDecision(gateID string) (*GateDecision, error) {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	decision, ok := gm.evaluationHistory[gateID]
	if !ok {
		return nil, fmt.Errorf("no decision found for gate: %s", gateID)
	}
	return decision, nil
}

// GetAllDecisions retrieves all gate decisions
func (gm *GateManager) GetAllDecisions() map[string]*GateDecision {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	// Return a copy
	decisions := make(map[string]*GateDecision, len(gm.evaluationHistory))
	maps.Copy(decisions, gm.evaluationHistory)
	return decisions
}

// ResetGate clears the evaluation history for a gate
func (gm *GateManager) ResetGate(gateID string) {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	delete(gm.evaluationHistory, gateID)
}

// ResetAll clears all evaluation history
func (gm *GateManager) ResetAll() {
	gm.mu.Lock()
	defer gm.mu.Unlock()
	gm.evaluationHistory = make(map[string]*GateDecision)
}

// GateEvaluationResult contains the result of evaluating multiple gates
type GateEvaluationResult struct {
	// Allow indicates if progression is allowed
	Allow bool

	// Reason for the decision
	Reason string

	// Confidence in the decision
	Confidence float64

	// Action to take if blocked
	Action GateAction

	// BlockingGate is the ID of the gate that blocked (if any)
	BlockingGate string

	// Decisions from individual gates
	Decisions map[string]*GateDecision

	// Timestamp
	Timestamp time.Time

	// Metadata
	Metadata map[string]any
}

// Summary returns a human-readable summary
func (r *GateEvaluationResult) Summary() string {
	if r.Allow {
		return fmt.Sprintf("All gates passed (%d evaluated)", len(r.Decisions))
	}
	return fmt.Sprintf("Blocked by gate '%s': %s (action: %s)", r.BlockingGate, r.Reason, r.Action)
}

// GateStatistics contains statistics about gate evaluations
type GateStatistics struct {
	TotalEvaluations int
	Passed           int
	Blocked          int
	Errors           int
	ByType           map[GateType]*GateTypeStats
	ByGate           map[string]*GateStats
}

// GateTypeStats contains statistics for a gate type
type GateTypeStats struct {
	Total         int
	Passed        int
	Blocked       int
	AvgConfidence float64
}

// GateStats contains statistics for a specific gate
type GateStats struct {
	Evaluations   int
	Passed        int
	Blocked       int
	AvgConfidence float64
	LastDecision  *GateDecision
}

// GetStatistics returns statistics about gate evaluations
func (gm *GateManager) GetStatistics() *GateStatistics {
	gm.mu.RLock()
	defer gm.mu.RUnlock()

	stats := &GateStatistics{
		ByType: make(map[GateType]*GateTypeStats),
		ByGate: make(map[string]*GateStats),
	}

	for gateID, decision := range gm.evaluationHistory {
		stats.TotalEvaluations++

		if decision.Allow {
			stats.Passed++
		} else {
			stats.Blocked++
		}

		// Get gate for type info
		if gate, ok := gm.gates[gateID]; ok {
			gateType := gate.Type()

			// Update type stats
			typeStats, ok := stats.ByType[gateType]
			if !ok {
				typeStats = &GateTypeStats{}
				stats.ByType[gateType] = typeStats
			}
			typeStats.Total++
			if decision.Allow {
				typeStats.Passed++
			} else {
				typeStats.Blocked++
			}
			typeStats.AvgConfidence = (typeStats.AvgConfidence*float64(typeStats.Total-1) + decision.Confidence) / float64(typeStats.Total)

			// Update gate stats
			gateStats, ok := stats.ByGate[gateID]
			if !ok {
				gateStats = &GateStats{}
				stats.ByGate[gateID] = gateStats
			}
			gateStats.Evaluations++
			if decision.Allow {
				gateStats.Passed++
			} else {
				gateStats.Blocked++
			}
			gateStats.AvgConfidence = (gateStats.AvgConfidence*float64(gateStats.Evaluations-1) + decision.Confidence) / float64(gateStats.Evaluations)
			gateStats.LastDecision = decision
		}
	}

	return stats
}
