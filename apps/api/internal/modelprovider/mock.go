package modelprovider

import "context"

type Mock struct{}

func NewMock() *Mock {
	return &Mock{}
}

func (*Mock) Complete(_ context.Context, request Request) (Response, error) {
	return Response{
		Text: "Mock 回复：" + request.Prompt,
		Usage: map[string]int{
			"promptTokens":     0,
			"completionTokens": 0,
			"totalTokens":      0,
		},
	}, nil
}
