// Package errtree 判断聚合错误树的每个分支是否都满足同一条件。
//
// 停机、关闭和重连判断需要区分“全部是取消/关闭”与“取消中混有真实故障”；
// 本包统一这类判断的遍历语义，避免各领域各写一份并在 nil、空聚合上出现差异。
package errtree

import "errors"

// Every 报告 err 的每个分支是否都满足 match。
//
// 沿 Unwrap() error 单链向下查找聚合节点（Unwrap() []error）；遇到聚合时要求每个
// 非 nil 子错误递归满足 match。没有聚合的单链以链顶错误调用一次 match，因此 match
// 内的 errors.Is/As 仍按标准语义沿链查找，中间节点自定义的 Is/As 也会生效。
// err 为 nil，或聚合中不含任何非 nil 子错误时返回 false。
func Every(err error, match func(error) bool) bool {
	for cause := err; cause != nil; {
		switch node := cause.(type) {
		case interface{ Unwrap() []error }:
			matched := false
			for _, child := range node.Unwrap() {
				if child == nil {
					continue
				}
				if !Every(child, match) {
					return false
				}
				matched = true
			}
			return matched
		case interface{ Unwrap() error }:
			cause = node.Unwrap()
		default:
			cause = nil
		}
	}
	return err != nil && match(err)
}

// Only 报告 err 的每个分支是否都 errors.Is(target)；语义同 Every。
func Only(err, target error) bool {
	return Every(err, func(err error) bool { return errors.Is(err, target) })
}
