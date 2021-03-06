//go:generate go run github.com/mailru/easyjson/easyjson -build_tags linux $GOFILE

// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// +build linux

package probe

import (
	"encoding/json"
	"time"

	"github.com/DataDog/datadog-agent/pkg/security/model"
	"github.com/DataDog/datadog-agent/pkg/security/rules"
	"github.com/DataDog/datadog-agent/pkg/security/secl/eval"
)

const (
	// LostEventsRuleID is the rule ID for the lost_events_* events
	LostEventsRuleID = "lost_events"
	// RulesetLoadedRuleID is the rule ID for the ruleset_loaded events
	RulesetLoadedRuleID = "ruleset_loaded"
	// NoisyProcessRuleID is the rule ID for the noisy_process events
	NoisyProcessRuleID = "noisy_process"
	// AbnormalPathRuleID is the rule ID for the abnormal_path events
	AbnormalPathRuleID = "abnormal_path"
)

// AllCustomRuleIDs returns the list of custom rule IDs
func AllCustomRuleIDs() []string {
	return []string{
		LostEventsRuleID,
		RulesetLoadedRuleID,
		NoisyProcessRuleID,
		AbnormalPathRuleID,
	}
}

func newCustomEvent(eventType model.EventType, marshalFunc func() ([]byte, error)) *CustomEvent {
	return &CustomEvent{
		eventType:   eventType,
		marshalFunc: marshalFunc,
	}
}

// CustomEvent is used to send custom security events to Datadog
type CustomEvent struct {
	eventType   model.EventType
	tags        []string
	marshalFunc func() ([]byte, error)
}

// Clone returns a copy of the current CustomEvent
func (ce *CustomEvent) Clone() CustomEvent {
	return CustomEvent{
		eventType:   ce.eventType,
		tags:        ce.tags,
		marshalFunc: ce.marshalFunc,
	}
}

// GetTags returns the tags of the custom event
func (ce *CustomEvent) GetTags() []string {
	return append(ce.tags, "type:"+ce.GetType())
}

// GetType returns the type of the custom event as a string
func (ce *CustomEvent) GetType() string {
	return ce.eventType.String()
}

// GetEventType returns the event type
func (ce *CustomEvent) GetEventType() model.EventType {
	return ce.eventType
}

// MarshalJSON is the JSON marshaller function of the custom event
func (ce *CustomEvent) MarshalJSON() ([]byte, error) {
	return ce.marshalFunc()
}

// String returns the string representation of a custom event
func (ce *CustomEvent) String() string {
	d, err := json.Marshal(ce)
	if err != nil {
		return err.Error()
	}
	return string(d)
}

func newRule(ruleDef *rules.RuleDefinition) *rules.Rule {
	return &rules.Rule{
		Rule:       &eval.Rule{ID: ruleDef.ID},
		Definition: ruleDef,
	}
}

// EventLostRead is the event used to report lost events detected from user space
// easyjson:json
type EventLostRead struct {
	Timestamp time.Time     `json:"date"`
	Name      string        `json:"map"`
	Lost      map[int]int64 `json:"per_cpu"`
}

// NewEventLostReadEvent returns the rule and a populated custom event for a lost_events_read event
func NewEventLostReadEvent(mapName string, perCPU map[int]int64) (*rules.Rule, *CustomEvent) {
	return newRule(&rules.RuleDefinition{
			ID: LostEventsRuleID,
		}), newCustomEvent(model.CustomLostReadEventType, EventLostRead{
			Name:      mapName,
			Lost:      perCPU,
			Timestamp: time.Now(),
		}.MarshalJSON)
}

// EventLostWrite is the event used to report lost events detected from kernel space
// easyjson:json
type EventLostWrite struct {
	Timestamp time.Time                 `json:"date"`
	Name      string                    `json:"map"`
	Lost      map[string]map[int]uint64 `json:"per_event_per_cpu"`
}

// NewEventLostWriteEvent returns the rule and a populated custom event for a lost_events_write event
func NewEventLostWriteEvent(mapName string, perEventPerCPU map[string]map[int]uint64) (*rules.Rule, *CustomEvent) {
	return newRule(&rules.RuleDefinition{
			ID: LostEventsRuleID,
		}), newCustomEvent(model.CustomLostWriteEventType, EventLostWrite{
			Name:      mapName,
			Lost:      perEventPerCPU,
			Timestamp: time.Now(),
		}.MarshalJSON)
}

// RulesetLoadedEvent is used to report that a new ruleset was loaded
// easyjson:json
type RulesetLoadedEvent struct {
	Timestamp time.Time         `json:"date"`
	Policies  map[string]string `json:"policies"`
	Rules     []rules.RuleID    `json:"rules"`
	Macros    []rules.MacroID   `json:"macros"`
}

// NewRuleSetLoadedEvent returns the rule and a populated custom event for a new_rules_loaded event
func NewRuleSetLoadedEvent(loadedPolicies map[string]string, loadedRules []rules.RuleID, loadedMacros []rules.MacroID) (*rules.Rule, *CustomEvent) {
	return newRule(&rules.RuleDefinition{
			ID: RulesetLoadedRuleID,
		}), newCustomEvent(model.CustomRulesetLoadedEventType, RulesetLoadedEvent{
			Timestamp: time.Now(),
			Policies:  loadedPolicies,
			Rules:     loadedRules,
			Macros:    loadedMacros,
		}.MarshalJSON)
}

// NoisyProcessEvent is used to report that a noisy process was temporarily discarded
// easyjson:json
type NoisyProcessEvent struct {
	Timestamp      time.Time                 `json:"date"`
	Event          string                    `json:"event_type"`
	Count          uint64                    `json:"pid_count"`
	Threshold      int64                     `json:"threshold"`
	ControlPeriod  time.Duration             `json:"control_period"`
	DiscardedUntil time.Time                 `json:"discarded_until"`
	Process        *ProcessContextSerializer `json:"process"`
}

// NewNoisyProcessEvent returns the rule and a populated custom event for a noisy_process event
func NewNoisyProcessEvent(eventType model.EventType,
	count uint64,
	threshold int64,
	controlPeriod time.Duration,
	discardedUntil time.Time,
	process *model.ProcessCacheEntry,
	resolvers *Resolvers,
	timestamp time.Time) (*rules.Rule, *CustomEvent) {
	return newRule(&rules.RuleDefinition{
			ID: NoisyProcessRuleID,
		}), newCustomEvent(model.CustomNoisyProcessEventType, NoisyProcessEvent{
			Timestamp:      timestamp,
			Event:          eventType.String(),
			Count:          count,
			Threshold:      threshold,
			ControlPeriod:  controlPeriod,
			DiscardedUntil: discardedUntil,
			Process:        newProcessContextSerializer(process, nil, resolvers),
		}.MarshalJSON)
}

func resolutionErrorToEventType(err error) model.EventType {
	switch err.(type) {
	case ErrTruncatedParents:
		return model.CustomTruncatedParentsEventType
	case ErrTruncatedSegment:
		return model.CustomTruncatedSegmentEventType
	default:
		return model.UnknownEventType
	}
}

// AbnormalPathEvent is used to report that a path resolution failed for a suspicious reason
// easyjson:json
type AbnormalPathEvent struct {
	Timestamp           time.Time        `json:"date"`
	Event               *EventSerializer `json:"triggering_event"`
	PathResolutionError string           `json:"path_resolution_error"`
}

// NewAbnormalPathEvent returns the rule and a populated custom event for a abnormal_path event
func NewAbnormalPathEvent(event *Event, pathResolutionError error) (*rules.Rule, *CustomEvent) {
	return newRule(&rules.RuleDefinition{
			ID: AbnormalPathRuleID,
		}), newCustomEvent(resolutionErrorToEventType(event.GetPathResolutionError()), AbnormalPathEvent{
			Timestamp:           event.ResolveEventTimestamp(),
			Event:               newEventSerializer(event),
			PathResolutionError: pathResolutionError.Error(),
		}.MarshalJSON)
}
