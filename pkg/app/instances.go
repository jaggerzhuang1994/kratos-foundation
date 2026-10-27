package app

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/go-kratos/kratos/v2/transport"
)

const (
	// MetadataAppName 是注册实例上标识所属应用名的元数据键。
	MetadataAppName = "app_name"
	// MetadataProcessID 是注册实例上标识所属进程实例 ID 的元数据键；同一进程注册的多个实例取值相同。
	MetadataProcessID = "process_id"
)

// registrationSource 是一个可注册 Runtime 的端点及其承载的服务名。
type registrationSource struct {
	// endpoint 是 Runtime 公布的端点。
	endpoint string
	// names、unmapped、err 来自 ServiceNamer；未实现该接口的 Runtime 以 unmapped 记录。
	names    []string
	unmapped []string
	err      error
}

// buildInstances 按登记顺序解析可注册 Runtime 的端点与服务名，并按服务名生成注册实例。
// RegistrationExempt 的 Runtime 与未实现 Endpointer 的 Runtime 不参与注册。
func (a *App) buildInstances() ([]*registry.ServiceInstance, error) {
	sources := make([]registrationSource, 0, len(a.runtimes))
	endpoints := make([]string, 0, len(a.runtimes))
	for _, runtime := range a.runtimes {
		if exempt, ok := runtime.(RegistrationExempt); ok && exempt.RegistrationExempt() {
			continue
		}
		endpointer, ok := runtime.(transport.Endpointer)
		if !ok {
			continue
		}
		endpoint, err := endpointer.Endpoint()
		if err != nil {
			return nil, err
		}
		source := registrationSource{endpoint: endpoint.String()}
		if namer, ok := runtime.(ServiceNamer); ok {
			source.names, source.unmapped, source.err = namer.ServiceNames()
		} else {
			source.unmapped = []string{fmt.Sprintf("runtime %T does not report service names", runtime)}
		}
		sources = append(sources, source)
		endpoints = append(endpoints, source.endpoint)
	}
	a.mu.Lock()
	a.endpoints = endpoints
	a.mu.Unlock()

	// 不注册服务时只解析端点，不推导服务名，也不输出注册计划。
	if a.registrar == nil {
		return nil, nil
	}
	return a.serviceInstances(sources)
}

// serviceInstances 按服务名分组端点：每个服务名挂载承载它的端点，未映射的业务入口归入应用名并告警。
// 没有端点的名字不注册，因此 Queue、Cron 等不承载业务服务的进程不注册。
func (a *App) serviceInstances(sources []registrationSource) ([]*registry.ServiceInstance, error) {
	byName := make(map[string][]string)
	addEndpoint := func(name, endpoint string) {
		if !slices.Contains(byName[name], endpoint) {
			byName[name] = append(byName[name], endpoint)
		}
	}
	var errs []error
	var unmapped []string
	for _, source := range sources {
		if source.err != nil {
			errs = append(errs, source.err)
		}
		for _, name := range source.names {
			addEndpoint(name, source.endpoint)
		}
		if len(source.unmapped) > 0 {
			addEndpoint(a.name, source.endpoint)
			unmapped = append(unmapped, source.unmapped...)
		}
	}
	if len(errs) > 0 {
		return nil, fmt.Errorf("resolve service names for registration: %w", errors.Join(errs...))
	}
	if len(unmapped) > 0 {
		a.logEvent(a.parent, kratoslog.LevelWarn, "app.registration.unmapped",
			"unmapped entries registered under application name",
			"app_name", a.name, "entries", unmapped)
	}

	names := make([]string, 0, len(byName))
	for name := range byName {
		names = append(names, name)
	}
	// 应用名实例排在最前，其余按名字排序，注册顺序稳定。
	sort.Slice(names, func(i, j int) bool {
		if (names[i] == a.name) != (names[j] == a.name) {
			return names[i] == a.name
		}
		return names[i] < names[j]
	})
	metadata := maps.Clone(a.metadata)
	if metadata == nil {
		metadata = make(map[string]string, 2)
	}
	metadata[MetadataAppName], metadata[MetadataProcessID] = a.name, a.id
	instances := make([]*registry.ServiceInstance, 0, len(names))
	summary := make([]string, 0, len(names))
	for _, name := range names {
		id := a.id
		if name != a.name {
			id = a.id + "@" + name
		}
		instances = append(instances, &registry.ServiceInstance{ID: id, Name: name, Version: a.version, Metadata: metadata, Endpoints: byName[name]})
		summary = append(summary, name+"="+strings.Join(byName[name], ","))
	}
	a.logEvent(a.parent, kratoslog.LevelInfo, "app.registration.planned", "service instances planned", "instances", summary)
	return instances, nil
}
