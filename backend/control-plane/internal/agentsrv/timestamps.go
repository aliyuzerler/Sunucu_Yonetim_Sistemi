package agentsrv

import (
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

func timestamppbNow() *timestamppb.Timestamp {
	return timestamppb.New(time.Now().UTC())
}

func timestamppbAfter(d time.Duration) *timestamppb.Timestamp {
	return timestamppb.New(time.Now().UTC().Add(d))
}

func timestamppbTime(t time.Time) *timestamppb.Timestamp {
	return timestamppb.New(t)
}
