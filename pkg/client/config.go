package client

import (
	"context"
	"github.com/jaggerzhuang1994/kratos-foundation/v2/proto/kratos_foundation_pb/config_pb"
	"google.golang.org/protobuf/proto"
)

func (f *factory) updateConfigActive(next *config_pb.Client) error {
	if err := f.builder.validateConfig(next); err != nil {
		return err
	}
	next = proto.CloneOf(next)
	return f.applyValidatedConfig(next)
}

func (f *factory) applyValidatedConfig(next *config_pb.Client) error {
	var (
		cancels []context.CancelFunc
		retired []retiredClient
	)
	nextSpecs := make(map[string]clientSpec, len(next.GetClients()))
	for name, option := range next.GetClients() {
		nextSpecs[name] = newClientSpec(name, option)
	}

	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return ErrFactoryClosed
	}

	current := f.config
	timeoutChanged := !proto.Equal(current.GetCleanupTimeout(), next.GetCleanupTimeout())
	currentClients := current.GetClients()
	nextClients := next.GetClients()
	names := make(map[string]struct{}, len(currentClients)+len(nextClients)+len(f.slots))
	for name := range currentClients {
		names[name] = struct{}{}
	}
	for name := range nextClients {
		names[name] = struct{}{}
	}
	for name := range f.slots {
		names[name] = struct{}{}
	}

	for name := range names {
		_, currentPresent := currentClients[name]
		_, nextPresent := nextClients[name]
		slot := f.slots[name]
		currentSpec := newClientSpec(name, nil)
		if slot != nil {
			currentSpec = slot.current.spec
		}
		nextSpec, configured := nextSpecs[name]
		if !configured {
			nextSpec = newClientSpec(name, nil)
		}
		if currentSpec.equal(nextSpec) {
			continue
		}

		if slot == nil {
			slot = &clientSlot{
				name: name,
				current: &clientVersion{
					revision: 2,
					spec:     nextSpec,
				},
			}
			f.slots[name] = slot
			continue
		}
		old := slot.current
		slot.current = &clientVersion{
			revision: old.revision + 1,
			spec:     nextSpec,
		}
		old.retired = true
		old.retireReason = reasonForConfigChange(currentPresent, nextPresent)
		if slot.build != nil && slot.build.version == old && slot.build.cancel != nil {
			cancels = append(cancels, slot.build.cancel)
		}
		if old.references == 0 && old.client.present() {
			retired = append(retired, detachRetired(slot.name, old))
		}
	}
	// cleanup 预算在构造时冻结，更新只记录需重启，避免停机过程中改变预算。
	f.config = next
	f.mu.Unlock()

	if timeoutChanged {
		f.logger.Warn("Client cleanup timeout changed; restart the application to apply it")
	}
	for _, cancel := range cancels {
		cancel()
	}
	for _, client := range retired {
		f.closeAndLog(client)
	}
	return nil
}

func reasonForConfigChange(oldPresent, nextPresent bool) retireReason {
	if oldPresent && !nextPresent {
		return retireConfigRemoved
	}
	return retireConfigUpdated
}
