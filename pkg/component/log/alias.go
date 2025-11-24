package log

import "github.com/go-kratos/kratos/v2/log"

type Logger = log.Logger
type Helper = log.Helper

var WithMessageKey = log.WithMessageKey
var FilterLevel = log.FilterLevel
var FilterKey = log.FilterKey
var FilterValue = log.FilterValue
var FilterFunc = log.FilterFunc

var LevelDebug = log.LevelDebug
var LevelInfo = log.LevelInfo
var LevelWarn = log.LevelWarn
var LevelError = log.LevelError
var LevelFatal = log.LevelFatal
