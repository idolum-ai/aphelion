//go:build linux

package turn

import (
	"context"

	"github.com/idolum-ai/aphelion/core"
)

// DeliveryStageInput captures delivery-mode decisions for one turn result.
type DeliveryStageInput struct {
	Request        DeliveryRequest
	Deliver        bool
	RecordOutbound bool
}

// DeliveryStageCallbacks provide side-effectful delivery operations.
type DeliveryStageCallbacks struct {
	Send           func(ctx context.Context, msg core.OutboundMessage, replyWithVoice bool) (int64, string, error)
	RecordFinal    func(text string, media []core.Media, kind string)
	RecordOutbound func(ctx context.Context, messageID int64, kind string) error
	PostCommit     func(ctx context.Context) error
}

// RunDeliveryStage applies delivery/record/hook ordering for one turn.
func RunDeliveryStage(ctx context.Context, input DeliveryStageInput, callbacks DeliveryStageCallbacks) (*DeliveryResult, error) {
	if input.Request.Result == nil {
		return nil, nil
	}

	outboundID := input.Request.Result.RenderedID
	outboundType := input.Request.Result.RenderedType
	if !input.Deliver || (input.Request.Result.RenderedStream && outboundID != 0) {
		if callbacks.RecordFinal != nil {
			callbacks.RecordFinal(input.Request.Message.Text, input.Request.Message.Media, outboundType)
		}
		if input.RecordOutbound && outboundID != 0 && callbacks.RecordOutbound != nil {
			if err := callbacks.RecordOutbound(ctx, outboundID, outboundType); err != nil {
				return nil, err
			}
		}
		if callbacks.PostCommit != nil {
			if err := callbacks.PostCommit(ctx); err != nil {
				return nil, err
			}
		}
		return &DeliveryResult{
			MessageID: outboundID,
			Kind:      outboundType,
		}, nil
	}

	if callbacks.Send == nil {
		return nil, nil
	}
	deliveredID, deliveredType, err := callbacks.Send(ctx, input.Request.Message, input.Request.ReplyWithVoice)
	if err != nil {
		return nil, err
	}
	outboundID = deliveredID
	outboundType = deliveredType

	if callbacks.RecordFinal != nil {
		callbacks.RecordFinal(input.Request.Message.Text, input.Request.Message.Media, outboundType)
	}
	if input.RecordOutbound && callbacks.RecordOutbound != nil {
		if err := callbacks.RecordOutbound(ctx, outboundID, outboundType); err != nil {
			return nil, err
		}
	}
	if callbacks.PostCommit != nil {
		if err := callbacks.PostCommit(ctx); err != nil {
			return nil, err
		}
	}
	return &DeliveryResult{MessageID: outboundID, Kind: outboundType}, nil
}
