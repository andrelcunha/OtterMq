package amqp

import (
	"bytes"
	"fmt"

	"github.com/rs/zerolog/log"
)

type ChannelCloseContent struct {
	ReplyCode uint16
	ReplyText string
	ClassID   uint16
	MethodID  uint16
}

type ChannelOpenContent struct {
}

type ChannelFlowContent struct {
	Active bool
}

type ChannelFlowOkContent struct {
	Active bool
}

func createChannelOpenOkFrame(channel uint16) ([]byte, error) {
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(CHANNEL),
		MethodID: uint16(CHANNEL_OPEN_OK),
		Content: ContentList{
			KeyValuePairs: []KeyValue{
				{
					Key:   INT_LONG,
					Value: uint32(0),
				},
			},
		},
	}.FormatMethodFrame()
}

func createChannelFlowFrame(channel uint16, active bool) ([]byte, error) {
	activeKv := KeyValue{
		Key:   BIT,
		Value: active,
	}
	content := ContentList{
		KeyValuePairs: []KeyValue{activeKv},
	}
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(CHANNEL),
		MethodID: uint16(CHANNEL_FLOW),
		Content:  content,
	}.FormatMethodFrame()
}

func createChannelFlowOkFrame(channel uint16, active bool) ([]byte, error) {
	activeKv := KeyValue{
		Key:   BIT,
		Value: active,
	}
	content := ContentList{
		KeyValuePairs: []KeyValue{activeKv},
	}
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(CHANNEL),
		MethodID: uint16(CHANNEL_FLOW_OK),
		Content:  content,
	}.FormatMethodFrame()
}

func createChannelCloseFrame(channel, replyCode, classID, methodID uint16, replyText string) ([]byte, error) {
	replyCodeKv := KeyValue{
		Key:   INT_SHORT,
		Value: replyCode,
	}
	replyTextKv := KeyValue{
		Key:   STRING_SHORT,
		Value: truncateCloseReplyText(replyText),
	}
	classIDKv := KeyValue{
		Key:   INT_SHORT,
		Value: classID,
	}
	methodIDKv := KeyValue{
		Key:   INT_SHORT,
		Value: methodID,
	}
	content := ContentList{
		KeyValuePairs: []KeyValue{replyCodeKv, replyTextKv, classIDKv, methodIDKv},
	}
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(CHANNEL),
		MethodID: uint16(CHANNEL_CLOSE),
		Content:  content,
	}.FormatMethodFrame()
}

func createChannelCloseOkFrame(channel uint16) ([]byte, error) {
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(CHANNEL),
		MethodID: uint16(CHANNEL_CLOSE_OK),
		Content:  ContentList{},
	}.FormatMethodFrame()
}

func parseChannelMethod(methodID uint16, payload []byte) (interface{}, error) {
	switch methodID {
	case uint16(CHANNEL_OPEN):
		log.Debug().Msg("Received CHANNEL_OPEN frame")
		return parseChannelOpenFrame(payload)

	case uint16(CHANNEL_OPEN_OK):
		log.Debug().Msg("Received CHANNEL_OPEN_OK frame")
		return parseChannelOpenOkFrame(payload)

	case uint16(CHANNEL_FLOW):
		log.Debug().Msg("Received CHANNEL_FLOW frame")
		return parseChannelFlowFrame(payload)

	case uint16(CHANNEL_FLOW_OK):
		log.Debug().Msg("Received CHANNEL_FLOW_OK frame")
		return parseChannelFlowOkFrame(payload)

	case uint16(CHANNEL_CLOSE):
		log.Debug().Msg("Received CHANNEL_CLOSE frame")
		return parseChannelCloseFrame(payload)

	case uint16(CHANNEL_CLOSE_OK):
		log.Debug().Msg("Received CHANNEL_CLOSE_OK frame")
		return parseChannelCloseOkFrame(payload)

	default:
		return nil, fmt.Errorf("unknown method ID: %d", methodID)
	}
}

func parseChannelOpenFrame(payload []byte) (*RequestMethodMessage, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("payload too short")
	}
	request := &RequestMethodMessage{
		Content: nil,
	}

	return request, nil
}

func parseChannelOpenOkFrame(payload []byte) (*RequestMethodMessage, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("payload too short")
	}
	request := &RequestMethodMessage{
		Content: nil,
	}

	return request, nil
}

func parseChannelFlowFrame(payload []byte) (*RequestMethodMessage, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("payload too short")
	}
	buf := bytes.NewReader(payload)
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed reading active octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"active"}, true)
	active := flags["active"]
	return &RequestMethodMessage{
		Content: &ChannelFlowContent{
			Active: active,
		},
	}, nil
}

func parseChannelFlowOkFrame(payload []byte) (*RequestMethodMessage, error) {
	if len(payload) < 1 {
		return nil, fmt.Errorf("payload too short")
	}
	buf := bytes.NewReader(payload)
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed reading active octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"active"}, true)
	active := flags["active"]
	return &RequestMethodMessage{
		Content: &ChannelFlowOkContent{
			Active: active,
		},
	}, nil
}

func parseChannelCloseFrame(payload []byte) (*RequestMethodMessage, error) {
	if len(payload) < 6 {
		return nil, fmt.Errorf("frame too short")
	}

	buf := bytes.NewReader(payload)
	replyCode, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed decoding reply code: %v", err)
	}
	replyText, err := DecodeShortStr(buf)
	if err != nil {
		return nil, fmt.Errorf("failed decoding reply text: %v", err)
	}
	classID, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed decoding class id: %v", err)
	}
	methodID, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed decoding method id: %v", err)
	}

	log.Trace().
		Uint16("reply_code", replyCode).
		Str("reply_text", replyText).
		Uint16("class_id", classID).
		Uint16("method_id", methodID).
		Msg("Received CHANNEL_CLOSE frame")
	msg := &ChannelCloseContent{
		ReplyCode: replyCode,
		ReplyText: replyText,
		ClassID:   classID,
		MethodID:  methodID,
	}
	request := &RequestMethodMessage{
		Content: msg,
	}
	return request, nil
}

func parseChannelCloseOkFrame(payload []byte) (*RequestMethodMessage, error) {
	if len(payload) > 1 {
		return nil, fmt.Errorf("unexxpected payload length")
	}
	request := &RequestMethodMessage{
		Content: nil,
	}

	return request, nil
}
