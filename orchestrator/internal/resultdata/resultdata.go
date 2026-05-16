package resultdata

import "google.golang.org/protobuf/types/known/structpb"

type Builder struct {
	fields map[string]any
}

func New() Builder {
	return Builder{fields: map[string]any{}}
}

func From(fields map[string]any) Builder {
	builder := New()
	builder.Merge(fields)
	return builder
}

func (b Builder) Add(key string, value any) Builder {
	if key == "" || value == nil {
		return b
	}
	if b.fields == nil {
		b.fields = map[string]any{}
	}
	b.fields[key] = value
	return b
}

func (b Builder) AddStruct(key string, value *structpb.Struct) Builder {
	return b.Add(key, Map(value))
}

func (b Builder) Merge(fields map[string]any) Builder {
	if len(fields) == 0 {
		return b
	}
	if b.fields == nil {
		b.fields = map[string]any{}
	}
	for key, value := range fields {
		if key == "" || value == nil {
			continue
		}
		b.fields[key] = value
	}
	return b
}

func (b Builder) Map() map[string]any {
	if len(b.fields) == 0 {
		return nil
	}
	out := make(map[string]any, len(b.fields))
	for key, value := range b.fields {
		out[key] = value
	}
	return out
}

func (b Builder) Struct() *structpb.Struct {
	return Struct(b.Map())
}

func Struct(data map[string]any) *structpb.Struct {
	if len(data) == 0 {
		return nil
	}
	out, err := structpb.NewStruct(data)
	if err != nil {
		out, _ = structpb.NewStruct(map[string]any{"marshal_error": err.Error()})
	}
	return out
}

func Map(data *structpb.Struct) map[string]any {
	if data == nil {
		return nil
	}
	return data.AsMap()
}
