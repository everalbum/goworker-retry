package retry

import (
	"crypto/sha1"
	"fmt"
	"github.com/everalbum/go-resque"
	"github.com/everalbum/goworker"
	"github.com/garyburd/redigo/redis"
	"strings"
	"time"
)

type backoff struct {
	jobName         string
	worker          func(string, ...interface{}) error
	RetryLimit      int
	BackoffStrategy []int
}

func NewBackoff(jobName string, workerFunc func(string, ...interface{}) error) *backoff {
	eb := new(backoff)
	eb.jobName = jobName
	eb.worker = workerFunc

	// Default backoff strategy in seconds
	eb.BackoffStrategy = []int{0, 60, 600, 3600, 10800, 21600} // 0s, 1m, 10m, 1h, 3h, 6h
	eb.RetryLimit = len(eb.BackoffStrategy)
	return eb
}

func (eb backoff) WorkerFunc() func(string, ...interface{}) error {
	return func(queue string, args ...interface{}) error {
		retryKey := eb.retryKey(args)

		// Setup the attempt
		retryAttempt, err := eb.beginAttempt(retryKey)
		if err != nil {
			return err
		}

		// Run the job
		workerErr := eb.worker(queue, args...)

		// Get redis connection
		conn, err := goworker.GetConn()
		if err != nil {
			return err
		}
		defer goworker.PutConn(conn)

		// Success, just clear the retry key
		if workerErr == nil {
			conn.Do("DEL", retryKey)
			return nil
		}

		if retryAttempt >= eb.RetryLimit {
			// If we've retried too many times, give up
			conn.Do("DEL", retryKey)
		} else {
			// Otherwise schedule the retry attempt
			seconds := eb.retryDelay(retryAttempt)
			if seconds <= 0 {
				// If there's no delay, just enqueue it
				_, err = resque.Enqueue(conn.Conn, queue, eb.jobName, args...)
			} else {
				// Otherwise schedule it
				delay := time.Duration(seconds) * time.Second
				err = resque.EnqueueIn(conn.Conn, delay, queue, eb.jobName, args...)
			}

			if err != nil {
				return err
			}
		}

		// Wrap the error
		return fmt.Errorf("retry: attempt %d of %d failed: %s", retryAttempt, eb.RetryLimit, workerErr.Error())
	}
}

func (eb backoff) beginAttempt(retryKey string) (int, error) {
	conn, err := goworker.GetConn()
	if err != nil {
		return -1, err
	}
	defer goworker.PutConn(conn)

	// Create the retry key if not exists
	_, err = conn.Do("SETNX", retryKey, -1)
	if err != nil {
		return -1, err
	}

	// Increment the attempt we're on
	retryAttempt, err := redis.Int(conn.Do("INCR", retryKey))
	if err != nil {
		return -1, err
	}

	// Expire the retry key so we don't leave it hanging
	// (an hour after it was supposed to be removed)
	conn.Do("EXPIRE", retryKey, eb.retryDelay(retryAttempt)+3600)

	return retryAttempt, nil
}

func (eb backoff) retryDelay(attempt int) int {
	if attempt > (len(eb.BackoffStrategy) - 1) {
		attempt = len(eb.BackoffStrategy) - 1
	}
	return eb.BackoffStrategy[attempt]
}

func (eb backoff) retryKey(args []interface{}) string {
	parts := []string{"resque", "resque-retry", eb.jobName, eb.retryIdentifier(args)}
	return strings.Join(parts, ":")
}

func (eb backoff) retryIdentifier(args []interface{}) string {
	params := make([]string, len(args))
	for i, value := range args {
		params[i] = fmt.Sprintf("%v", value)
	}

	h := sha1.New()
	h.Write([]byte(strings.Join(params, "-")))
	bs := h.Sum(nil)

	hash := fmt.Sprintf("%x", bs)

	return strings.Replace(hash, " ", "", -1)
}
