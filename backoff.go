package retry

import (
	"crypto/sha1"
	"errors"
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

func (eb *backoff) WorkerFunc() func(string, ...interface{}) error {
	return func(queue string, args ...interface{}) error {
		conn, err := goworker.GetConn()
		if err != nil {
			return err
		}
		defer goworker.PutConn(conn)

		retryKey := eb.retryKey(args)

		// Create the retry key if not exists
		_, err = conn.Do("SETNX", retryKey, -1)
		if err != nil {
			return err
		}

		// Increment the attempt we're on
		retryAttempt, err := redis.Int(conn.Do("INCR", retryKey))
		if err != nil {
			return err
		}

		// Expire the retry key so we don't leave it hanging
		// (an hour after it was supposed to be removed)
		err = eb.worker(queue, args...)
		redis.Int(conn.Do("EXPIRE", retryKey, eb.retryDelay(retryAttempt)+3600))

		// Success, just clear the retry key
		if err == nil {
			conn.Do("DEL", retryKey)
			return nil
		}

		// If we've retried too many times, give up and return the err
		if retryAttempt >= eb.RetryLimit {
			conn.Do("DEL", retryKey)
			return errors.New(fmt.Sprintf("Failed after %d attempts: %s", (retryAttempt + 1), err.Error()))
		}

		// Schedule the retry attempt
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

		// NOTE (jonmumm)
		// By default, if the job failed we suppress errors
		// and assume we only want to report the error if it
		// still fails after all of its attempts. This may change
		// in the future (or be parameterized).
		return nil
	}
}

func (eb *backoff) retryDelay(attempt int) int {
	if attempt > (len(eb.BackoffStrategy) - 1) {
		attempt = len(eb.BackoffStrategy) - 1
	}
	return eb.BackoffStrategy[attempt]
}

func (eb *backoff) retryKey(args []interface{}) string {
	parts := []string{"resque", "resque-retry", eb.jobName, eb.retryIdentifier(args)}
	return strings.Join(parts, ":")
}

func (eb *backoff) retryIdentifier(args []interface{}) string {
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
