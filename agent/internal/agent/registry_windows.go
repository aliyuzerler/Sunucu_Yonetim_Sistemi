//go:build windows

package agent

import (
	"context"
	"encoding/json"

	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/broker"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winevent"
	"github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/agent/internal/winops"
	agentv1 "github.com/aliyuzerler/Sunucu_Yonetim_Sistemi/gen/proto/servercontrol/agent/v1"
)

// DefaultRegistry returns the Faz 05 windows executor set: the echo proof,
// the five SCM service operations (broker-backed over the named pipe, the
// broker runs as LocalSystem) and the DIRECT event-log reader (no privilege
// needed for the allowlisted channels — Security is excluded by design).
func DefaultRegistry(brokerPath string) (*ExecutorRegistry, error) {
	reader := &winevent.Reader{}
	return NewRegistry(
		EchoExecutor{},
		NewServiceExecutor(broker.OpWinServiceList, brokerPath),
		NewServiceExecutor(broker.OpWinServiceStatus, brokerPath),
		NewServiceExecutor(broker.OpWinServiceStart, brokerPath),
		NewServiceExecutor(broker.OpWinServiceStop, brokerPath),
		NewServiceExecutor(broker.OpWinServiceRestart, brokerPath),
		NewEventlogExecutor(reader),
	)
}

// EventlogExecutor executes windows.eventlog.read IN-PROCESS (wevtutil
// argv; the read-only channels need no elevation).
type EventlogExecutor struct {
	reader *winevent.Reader
}

// NewEventlogExecutor builds the event-log executor.
func NewEventlogExecutor(r *winevent.Reader) *EventlogExecutor { return &EventlogExecutor{reader: r} }

// Info describes the capability; availability is refined by ProbeAvailable.
func (e *EventlogExecutor) Info() *agentv1.Capability {
	return &agentv1.Capability{
		CapabilityId:   winops.OpWinEventlogRead,
		Version:        "1.0.0",
		Available:      false,
		AdapterId:      "wevtutil",
		AdapterVersion: Version,
	}
}

// ProbeAvailable does a cheap System-channel probe.
func (e *EventlogExecutor) ProbeAvailable(ctx context.Context) (bool, string) {
	return e.reader.ProbeAvailable(ctx)
}

// eventlogPayload is the agent-side mirror of the eventlog schema.
type eventlogPayload struct {
	Channel string `json:"channel"`
	Level   int    `json:"level,omitempty"`
	Count   int    `json:"count,omitempty"`
}

// Execute validates the params (closed enum + bounded ints) and reads.
func (e *EventlogExecutor) Execute(ctx context.Context, payload []byte) ([]byte, string, error) {
	var p eventlogPayload
	if err := decodeStrict(payload, &p); err != nil {
		return nil, "", err
	}
	params := winops.EventlogParams{Channel: p.Channel, Level: p.Level, Count: p.Count}
	if err := winops.ValidateEventlogParams(params); err != nil {
		return nil, "", err
	}
	events, err := e.reader.Read(ctx, params)
	if err != nil {
		return nil, "", err
	}
	body, err := json.Marshal(map[string]any{"events": events, "channel": params.Channel})
	if err != nil {
		return nil, "", err
	}
	return body, "olay günlüğü okundu", nil
}
