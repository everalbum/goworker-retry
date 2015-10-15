# goworker-retry

[![GoDoc](https://godoc.org/github.com/everalbum/goworker-retry?status.svg)](https://godoc.org/github.com/everalbum/goworker-retry)

Retry strategy for use with Resque and [goworker](https://www.goworker.org/).

## Installation

```
go get github.com/everalbum/goworker-retry
```

## Usage

```go
package main

import (
  "github.com/everalbum/goworker"
  "github.com/everalbum/goworker-retry"
)

func main() {
  myJob := "MyJob"
  retryWorker := retry.NewBackoff(myJob, myWorker)

  // Override default settings...
  retryWorker.RetryLimit = 6
  retryWorker.BackoffStrategy = []int{0, 60, 600, 3600, 10800, 21600} // 0s, 1m, 10m, 1h, 3h, 6h

  goworker.Register(myJob, retryWorker.WorkerFunc())

  if err := goworker.Work(); err != nil {
    fmt.Println("Error:", err)
  }
}

func myWorker(queue string, args ...interface{}) error {
  // Do work...
  return nil
}
```
