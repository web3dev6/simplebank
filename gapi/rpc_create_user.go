package gapi

import (
	"context"
	"time"

	"github.com/hibiken/asynq"
	db "github.com/web3dev6/simplebank/db/sqlc"
	"github.com/web3dev6/simplebank/pb"
	"github.com/web3dev6/simplebank/util"
	"github.com/web3dev6/simplebank/worker"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (server *Server) CreateUser(ctx context.Context, req *pb.CreateUserRequest) (*pb.CreateUserResponse, error) {
	// validate request & err handling
	violations := validateCreateUserRequest(req)
	if violations != nil {
		return nil, invalidArgumentError(violations)
	}

	// hash password
	hashedPassword, err := util.HashPassword(req.GetPassword())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to hash password: %s", err)
	}

	// make create_user params
	arg := db.CreateUserParams{
		Username:       req.GetUsername(),
		HashedPassword: hashedPassword,
		FullName:       req.GetFullName(),
		Email:          req.GetEmail(),
	}

	// call CreateUser for db
	user, err := server.store.CreateUser(ctx, arg)
	if err != nil {
		// username and email must be unique (UNIQUE)
		if db.ErrorCode(err) == db.UniqueViolation {
			return nil, status.Errorf(codes.AlreadyExists, "username or email already exists: %s", err)
		}
		return nil, status.Errorf(codes.Internal, "failed to create user: %s", err)
	}

	// send verification email to user
	taskPayload := &worker.PayloadSendVerifyEmail{
		Username: user.Username,
	}
	// todo: use db transaction as user shouldn't be created if taskDistributor fails - rollback
	// asynq options to configure task processing while putting it in queue
	opts := []asynq.Option{
		asynq.MaxRetry(10),                // retry fails 10 times
		asynq.ProcessIn(3 * time.Second),  // process in 3 secs
		asynq.Queue(worker.QueueCritical), // push in queue "critical"
	}
	err = server.taskDistributor.DistributeTaskSendVerifyEmail(ctx, taskPayload, opts...)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to distribute task TaskSendVerifyEmail: %s", err)
	}

	// return resp
	resp := &pb.CreateUserResponse{
		User: convertUser(user),
	}
	return resp, nil
}

func validateCreateUserRequest(req *pb.CreateUserRequest) (violations []*errdetails.BadRequest_FieldViolation) {
	if err := ValidateUsername(req.GetUsername()); err != nil {
		violations = append(violations, fieldViolation("username", err))
	}
	if err := ValidatePassword(req.GetPassword()); err != nil {
		violations = append(violations, fieldViolation("password", err))
	}
	if err := ValidateEmail(req.GetEmail()); err != nil {
		violations = append(violations, fieldViolation("email", err))
	}
	if err := ValidateFullname(req.GetFullName()); err != nil {
		violations = append(violations, fieldViolation("full_name", err))
	}
	return violations
}
