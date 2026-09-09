// Package database 统一管理 GORM 连接池、具名连接选择、事务与数据库可观测性。
//
// 业务代码只需依赖 Manager；连接池的创建与释放由 NewManager 返回的
// cleanup 统一负责，避免多个组件分别掌握同一连接的生命周期。
// GORM 插件和连接级配置都是本包的内部实现。
//
// Model 字段声明为 AESDecryptString 或 AESDecryptBytes 后，GORM 会按实际使用的
// DBConnection AES 配置自动加密写入、解密读出。AES 配置是可选的；未配置不会影响
// 普通字段，但操作上述加密字段时会返回 ErrAESConfigMissing。
package database
