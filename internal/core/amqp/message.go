package amqp

import (
	"bytes"
	"encoding/binary"

	"github.com/rs/zerolog/log"
)

const (
	FRAME_END = 0xCE
)

type ChannelState struct {
	MethodFrame    *RequestMethodMessage
	HeaderFrame    *HeaderFrame
	Body           []byte
	BodySize       uint64
	ClosingChannel bool
}

type HeaderFrame struct {
	Channel    uint16
	ClassID    uint16
	BodySize   uint64
	Properties *BasicProperties
}

type RequestMethodMessage struct {
	Channel  uint16
	ClassID  uint16
	MethodID uint16
	Content  interface{}
}

type ResponseMethodMessage struct {
	Channel  uint16
	ClassID  uint16
	MethodID uint16
	Content  ContentList
}

type ResponseContent struct {
	Channel uint16
	ClassID uint16
	Weight  uint16
	Message Message
}

type Message struct {
	ID         string          `json:"id"`
	Body       []byte          `json:"body"`
	Properties BasicProperties `json:"properties"`
	Exchange   string          `json:"exchange"`
	RoutingKey string          `json:"routing_key"`
}

type ContentList struct {
	KeyValuePairs []KeyValue
}

type KeyValue struct {
	Key   string
	Value interface{}
}

func (rc ResponseContent) FormatHeaderFrame() []byte {
	return createHeaderFrame(rc.Channel, rc.ClassID, rc.Message)
}

func createHeaderFrame(channel, classID uint16, msg Message) []byte {
	frameType := uint8(TYPE_HEADER)
	var payloadBuf bytes.Buffer
	weight := uint16(0) // amqp-0-9-1 spec says "weight field is unused and must be zero"
	bodySize := len(msg.Body)
	flag_list, flags, err := msg.Properties.encodeBasicProperties()
	if err != nil {
		log.Error().Err(err).Msg("Error")
		return nil
	}

	_ = binary.Write(&payloadBuf, binary.BigEndian, uint16(classID))  // Error ignored as bytes.Buffer.Write never fails
	_ = binary.Write(&payloadBuf, binary.BigEndian, uint16(weight))   // Error ignored as bytes.Buffer.Write never fails
	_ = binary.Write(&payloadBuf, binary.BigEndian, uint64(bodySize)) // Error ignored as bytes.Buffer.Write never fails
	_ = binary.Write(&payloadBuf, binary.BigEndian, uint16(flags))    // Error ignored as bytes.Buffer.Write never fails
	payloadBuf.Write(flag_list)

	payloadSize := uint32(payloadBuf.Len())

	headerBuf := formatHeader(frameType, channel, payloadSize)

	frame := append(headerBuf, payloadBuf.Bytes()...)
	frame = append(frame, FRAME_END)
	return frame
}

func (rc ResponseContent) FormatBodyFrame(frameMax uint32) []byte {
	return createBodyFrame(rc.Channel, frameMax, rc.Message.Body)
}

// createBodyFrame returns content as consecutive body frames, each at most
// frameMax octets long (header and frame-end included). frameMax 0 means no
// limit. An empty body still yields one empty body frame.
func createBodyFrame(channel uint16, frameMax uint32, content []byte) []byte {
	frameType := uint8(TYPE_BODY)
	chunkSize := len(content)
	if frameMax > FRAME_OVERHEAD && uint64(frameMax-FRAME_OVERHEAD) < uint64(chunkSize) {
		chunkSize = int(frameMax - FRAME_OVERHEAD)
	}
	if chunkSize == 0 {
		return append(formatHeader(frameType, channel, 0), FRAME_END)
	}

	frames := (len(content) + chunkSize - 1) / chunkSize
	out := make([]byte, 0, len(content)+frames*FRAME_OVERHEAD)
	for start := 0; start < len(content); start += chunkSize {
		chunk := content[start:min(start+chunkSize, len(content))]
		out = append(out, formatHeader(frameType, channel, uint32(len(chunk)))...)
		out = append(out, chunk...)
		out = append(out, FRAME_END)
	}
	return out
}

func (msg ResponseMethodMessage) FormatMethodFrame() []byte {
	var payloadBuf bytes.Buffer
	class := msg.ClassID
	method := msg.MethodID
	channelNum := msg.Channel

	_ = binary.Write(&payloadBuf, binary.BigEndian, uint16(class))  // Error ignored as bytes.Buffer.Write never fails
	_ = binary.Write(&payloadBuf, binary.BigEndian, uint16(method)) // Error ignored as bytes.Buffer.Write never fails

	var methodPayload []byte
	if len(msg.Content.KeyValuePairs) > 0 {
		methodPayload = formatMethodPayload(msg.Content)
	}
	payloadBuf.Write(methodPayload)

	// Calculate the size of the payload
	payloadSize := uint32(payloadBuf.Len())

	// Buffer for the frame header
	frameType := uint8(TYPE_METHOD) // METHOD frame type
	headerBuf := formatHeader(frameType, channelNum, payloadSize)

	frame := append(headerBuf, payloadBuf.Bytes()...)

	frame = append(frame, FRAME_END) // frame-end

	return frame
}

func formatMethodPayload(content ContentList) []byte {
	var payloadBuf bytes.Buffer
	for _, kv := range content.KeyValuePairs {
		switch kv.Key {
		case INT_OCTET:
			_ = binary.Write(&payloadBuf, binary.BigEndian, kv.Value.(uint8)) // Error ignored as bytes.Buffer.Write never fails
		case INT_SHORT:
			_ = binary.Write(&payloadBuf, binary.BigEndian, kv.Value.(uint16)) // Error ignored as bytes.Buffer.Write never fails
		case INT_LONG:
			_ = binary.Write(&payloadBuf, binary.BigEndian, kv.Value.(uint32)) // Error ignored as bytes.Buffer.Write never fails
		case INT_LONG_LONG:
			_ = binary.Write(&payloadBuf, binary.BigEndian, kv.Value.(uint64)) // Error ignored as bytes.Buffer.Write never fails
		case BIT:
			if kv.Value.(bool) {
				payloadBuf.WriteByte(1)
			} else {
				payloadBuf.WriteByte(0)
			}
		case STRING_SHORT:
			EncodeShortStr(&payloadBuf, kv.Value.(string))
		case STRING_LONG:
			EncodeLongStr(&payloadBuf, kv.Value.([]byte))
		case TIMESTAMP:
			_ = binary.Write(&payloadBuf, binary.BigEndian, kv.Value.(int64)) // Error ignored as bytes.Buffer.Write never fails
		case TABLE:
			encodedTable := EncodeTable(kv.Value.(map[string]any))
			EncodeLongStr(&payloadBuf, encodedTable)
		}
	}
	return payloadBuf.Bytes()
}

func formatHeader(frameType uint8, channel uint16, payloadSize uint32) []byte {
	header := make([]byte, 7)
	header[0] = frameType
	binary.BigEndian.PutUint16(header[1:3], channel)
	binary.BigEndian.PutUint32(header[3:7], uint32(payloadSize))
	return header
}

func EncodeGetOkToContentList(content *BasicGetOkContent) *ContentList {
	KeyValuePairs := []KeyValue{
		{ // delivery_tag
			Key:   INT_LONG_LONG,
			Value: content.DeliveryTag,
		},
		{ // redelivered
			Key:   BIT,
			Value: content.Redelivered,
		},
		{ // exchange
			Key:   STRING_SHORT,
			Value: content.Exchange,
		},
		{ // routing_key
			Key:   STRING_SHORT,
			Value: content.RoutingKey,
		},
		{ // message_count
			Key:   INT_LONG,
			Value: content.MessageCount,
		},
	}
	contentList := &ContentList{KeyValuePairs: KeyValuePairs}
	return contentList
}
