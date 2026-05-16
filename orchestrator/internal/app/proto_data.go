package app

import (
	"orchestrator/internal/resultdata"

	"google.golang.org/protobuf/types/known/structpb"
)

func protoData(data map[string]any) *structpb.Struct {
	return resultdata.Struct(data)
}

func protoDataMap(data *structpb.Struct) map[string]any {
	return resultdata.Map(data)
}
