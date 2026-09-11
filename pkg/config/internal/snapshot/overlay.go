package snapshot

// overlaySource 将后一个配置源叠加到此前累积的配置上，键名精确匹配。
// 对象递归覆盖；数组、标量和显式 null 整体替换。
// 两棵树必须由本次构建独占且尚未发布：原地更新 accumulated，并接管 sourceValue 的子树。
// Source 原始值在解析和 normalize 后已独立，不能把已发布 Snapshot 的值传入这里。
func overlaySource(accumulated, sourceValue any) any {
	baseMap, baseOK := accumulated.(map[string]any)
	overrideMap, overrideOK := sourceValue.(map[string]any)
	if !baseOK || !overrideOK {
		return sourceValue
	}

	for key, override := range overrideMap {
		if current, ok := baseMap[key]; ok {
			baseMap[key] = overlaySource(current, override)
		} else {
			baseMap[key] = override
		}
	}
	return baseMap
}
