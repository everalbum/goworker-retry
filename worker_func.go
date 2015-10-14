package retry

type workerFunc func(string, ...interface{}) error
