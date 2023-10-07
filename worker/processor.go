package worker

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog/log"
	db "github.com/web3dev6/simplebank/db/sqlc"
)

/*
   Pick up task from Redis queue & process them
   Note* Must register task@TaskSendVerifyEmail with asynq server,
        this tells asynq - the task has to be run by which handler function
        code for this task-registration written in Start()
*/

const (
	QueueCritical              = "critical"
	QueueDefault               = "default"
	QueueLow                   = "low"
	TaskRetryDurationInSeconds = 3
)

// Makes code more generic & easier to mock and test
type TaskProcessor interface {
	Start() error
	ProcessTaskSendVerifyEmail(ctx context.Context, task *asynq.Task) error
}

// RedisTaskProcessor implements TaskProcessor
type RedisTaskProcessor struct {
	server *asynq.Server
	store  db.Store
}

// interface as return type - forcing RedisTaskProcessor to implement TaskProcessor
func NewRedisTaskProcessor(redisOpt asynq.RedisClientOpt, store db.Store) TaskProcessor {
	// our custom Logger instance
	logger := NewLogger()
	// call SetLogger to set our custom Logger struct as implementation for Redis Logging interface
	redis.SetLogger(logger)
	// asynq.Config{} allows us to control many different parameters of the asynq server
	// Note* Queue name must be given in Queues Config
	//		if not, the server will process only the "default" queue
	server := asynq.NewServer(redisOpt,
		asynq.Config{
			Queues: map[string]int{
				QueueCritical: 10,
				QueueDefault:  5,
				QueueLow:      1,
			},
			// ErrorHandler handles when task fails and returns error
			ErrorHandler: asynq.ErrorHandlerFunc(func(ctx context.Context, task *asynq.Task, err error) {
				// log failed task details
				log.Error().Err(err).
					Str("type", task.Type()).
					RawJSON("payload", task.Payload()).
					// Bytes("payload", task.Payload()).
					Msg("process task failed")
			}),
			// RetryDelayFunc defines the retry interval - constant of 3secs for now
			RetryDelayFunc: asynq.RetryDelayFunc(func(n int, e error, t *asynq.Task) time.Duration {
				return time.Duration(TaskRetryDurationInSeconds * time.Second)
			}),
			// Logger - logger implements the asynq's Logger interface with zerolog logging at various log levels
			Logger: logger,
		})
	return &RedisTaskProcessor{
		server: server,
		store:  store,
	}
}

// ProcessTaskSendVerifyEmail - processes the read  SendVerifyEmail task
// Note* asynq has already taken care of the core part of pulling task from Redis & feed it to the bg-worker to process it via the task parameter of below handler func
func (processor *RedisTaskProcessor) ProcessTaskSendVerifyEmail(ctx context.Context, task *asynq.Task) error {
	// parse the task to get the payload - deserialize
	var payload PayloadSendVerifyEmail
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		// Note* if not unmarshalable, no point of re-trying, tell the same to asynq by wrapping the asynq.SkipRetry error
		return fmt.Errorf("failed to unmarshal task payload: %w", asynq.SkipRetry)
	}

	// process the task - Get user from db and send welcome email
	user, err := processor.store.GetUser(ctx, payload.Username)
	if err != nil {
		if errors.Is(err, db.ErrRecordNotFound) || errors.Is(err, sql.ErrNoRows) {
			// user doesn't exist, no need to retry
			return fmt.Errorf("user with username %s doesn't exist: %w", payload.Username, asynq.SkipRetry)
		}
		return fmt.Errorf("failed to get user with username %s: %w", payload.Username, err)
	}
	// todo send email here

	// log processed task details
	log.Info().
		Str("type", task.Type()).
		RawJSON("payload", task.Payload()).
		Str("user_email", user.Email).
		Msg("processed task")

	return nil
}

// Start - we will register the task@TaskSendVerifyEmail  in this func before starting the asynq server
func (processor *RedisTaskProcessor) Start() error {
	mux := asynq.NewServeMux()
	// we can use this mux to register each task with its handler function, similar to http-mux
	// Register @TaskSendVerifyEmail
	mux.HandleFunc(TaskSendVerifyEmail, processor.ProcessTaskSendVerifyEmail)

	// start server
	return processor.server.Start(mux)
}
