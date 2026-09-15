package log

// outputChange 保存锁外准备的候选和资源复用关系。
type outputChange struct {
	owner          *outputLogger
	previous, next *preparedOutput
	reused         bool
}

// applyRuntimeConfig 乐观准备资源，再在统一提交边界发布；登记或策略变化时重试。
func (s *sharedState) applyRuntimeConfig(config *RuntimeConfig) error {
	if err := ValidateRuntimeConfig(config); err != nil {
		return err
	}
	policy := cloneRuntimeConfig(config)
	for {
		s.gate.RLock()
		epoch := s.epoch
		bases := make(map[*outputLogger]envConfig, len(s.owners))
		previous := make(map[*outputLogger]*preparedOutput, len(s.owners))
		for owner, base := range s.owners {
			bases[owner] = base
			previous[owner] = owner.preparedOutput
		}
		s.gate.RUnlock()
		changes := make([]outputChange, 0, len(bases))
		for owner, base := range bases {
			merged, err := mergeOutputConfig(base, policy)
			if err != nil {
				discardChanges(changes)
				return err
			}
			next, reused, err := prepareOutput(merged, previous[owner])
			if err != nil {
				discardChanges(changes)
				return err
			}
			changes = append(changes, outputChange{owner: owner, previous: previous[owner], next: next, reused: reused})
		}
		s.gate.Lock()
		if epoch != s.epoch {
			s.gate.Unlock()
			discardChanges(changes)
			continue
		}
		// 写锁等待旧策略的日志退出；这里不打开、关闭文件，也不执行用户回调。
		for _, change := range changes {
			change.owner.mu.Lock()
			change.owner.preparedOutput = change.next
			change.owner.mu.Unlock()
		}
		for {
			old := s.custom.Load()
			next := &customState{policy: policy, filterKeys: policy.FilterKeys, version: 1}
			if old != nil {
				next.version = old.version + 1
				next.kv = old.kv
			}
			if s.custom.CompareAndSwap(old, next) {
				break
			}
		}
		s.epoch++
		s.gate.Unlock()
		for _, change := range changes {
			if change.previous.ready != nil {
				<-change.previous.ready
			}
			if !change.reused && change.previous.releaseFile != nil {
				change.previous.releaseFile()
			}
			close(change.next.ready)
		}
		return nil
	}
}

// discardChanges 仅释放新建候选，复用的活动文件仍属于原实例。
func discardChanges(changes []outputChange) {
	for _, change := range changes {
		if !change.reused && change.next.releaseFile != nil {
			change.next.releaseFile()
		}
	}
}

func (s *sharedState) newOutput(base envConfig) (*outputLogger, func(), error) {
	for {
		s.gate.RLock()
		epoch := s.epoch
		current := s.custom.Load()
		var policy *RuntimeConfig
		if current != nil {
			policy = current.policy
		}
		s.gate.RUnlock()
		merged, err := mergeOutputConfig(base, policy)
		if err != nil {
			return nil, nil, err
		}
		out, release, err := newOutputLogger(merged)
		if err != nil {
			return nil, nil, err
		}
		s.gate.Lock()
		if epoch != s.epoch {
			s.gate.Unlock()
			release()
			continue
		}
		if s.owners == nil {
			s.owners = make(map[*outputLogger]envConfig)
		}
		s.owners[out] = base
		s.epoch++
		s.gate.Unlock()
		return out, func() {
			s.gate.Lock()
			if _, ok := s.owners[out]; !ok {
				s.gate.Unlock()
				return
			}
			delete(s.owners, out)
			s.epoch++
			s.gate.Unlock()
			release()
		}, nil
	}
}
