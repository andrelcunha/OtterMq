package amqp

import (
	"bytes"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
)

type BasicQosContent struct {
	PrefetchSize  uint32
	PrefetchCount uint16
	Global        bool
}

type BasicConsumeContent struct {
	Queue       string
	ConsumerTag string
	NoLocal     bool
	NoAck       bool
	Exclusive   bool
	NoWait      bool
	Arguments   map[string]any
}

type BasicCancelContent struct {
	ConsumerTag string
	NoWait      bool
}

type BasicPublishContent struct {
	Exchange   string
	RoutingKey string
	Mandatory  bool
	Immediate  bool
}

type BasicReturnContent struct {
	ReplyCode  uint16
	ReplyText  string
	Exchange   string
	RoutingKey string
}

type BasicGetMessageContent struct {
	Reserved1 uint16
	Queue     string
	NoAck     bool
}

type BasicGetOkContent struct {
	DeliveryTag  uint64
	Redelivered  bool
	Exchange     string
	RoutingKey   string
	MessageCount uint32
}

type BasicAckContent struct {
	DeliveryTag uint64
	Multiple    bool
}

type BasicRejectContent struct {
	DeliveryTag uint64
	Requeue     bool
}

type BasicNackContent struct {
	DeliveryTag uint64
	Multiple    bool
	Requeue     bool
}

type BasicRecoverContent struct {
	Requeue bool
	Async   bool
}

type BasicProperties struct {
	ContentType     ContentType    // shortstr
	ContentEncoding string         // shortstr
	Headers         map[string]any // table
	DeliveryMode    DeliveryMode   // from octet: (1=non-persistent, 2=persistent)
	Priority        uint8          // octet
	CorrelationID   string         // shortstr
	ReplyTo         string         // shortstr
	Expiration      string         // shortstr
	MessageID       string         // shortstr
	Timestamp       time.Time      // timestamp (64 bits)
	Type            string         // shortsrt
	UserID          string         // shortstr
	AppID           string         // shortstr
	Reserved        string         // shortstr
}

func createBasicQosOkFrame(channel uint16) ([]byte, error) {
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_QOS_OK),
		Content:  ContentList{},
	}.FormatMethodFrame()
}

func createBasicConsumeOkFrame(channel uint16, consumerTag string) ([]byte, error) {
	keyValuePairs := []KeyValue{
		{
			Key:   STRING_SHORT,
			Value: consumerTag,
		},
	}

	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_CONSUME_OK),
		Content:  ContentList{KeyValuePairs: keyValuePairs},
	}.FormatMethodFrame()
}

// createBasicCancelFrame builds the basic.cancel the server sends to a
// consumer it cancelled (consumer_cancel_notify). nowait is set: the client
// doesn't answer with cancel-ok.
func createBasicCancelFrame(channel uint16, consumerTag string) ([]byte, error) {
	keyValuePairs := []KeyValue{
		{
			Key:   STRING_SHORT,
			Value: consumerTag,
		},
		{
			Key:   BIT,
			Value: true, // nowait
		},
	}

	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_CANCEL),
		Content:  ContentList{KeyValuePairs: keyValuePairs},
	}.FormatMethodFrame()
}

func createBasicCancelOkFrame(channel uint16, consumerTag string) ([]byte, error) {
	keyValuePairs := []KeyValue{
		{
			Key:   STRING_SHORT,
			Value: consumerTag,
		},
	}

	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_CANCEL_OK),
		Content:  ContentList{KeyValuePairs: keyValuePairs},
	}.FormatMethodFrame()
}

// createBasicReturnFrame creates a Basic.Return (50) frame for the given message.
func createBasicReturnFrame(channel uint16, replyCode uint16, replyText, exchange, routingKey string) ([]byte, error) {
	KeyValuePairs := []KeyValue{
		{ // reply_code
			Key:   INT_SHORT,
			Value: replyCode,
		},
		{ // reply_text
			Key:   STRING_SHORT,
			Value: replyText,
		},
		{ // exchange
			Key:   STRING_SHORT,
			Value: exchange,
		},
		{ // routing_key
			Key:   STRING_SHORT,
			Value: routingKey,
		},
	}
	contentList := &ContentList{KeyValuePairs: KeyValuePairs}

	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_RETURN),
		Content:  *contentList,
	}.FormatMethodFrame()
}

// createBasicDeliverFrame creates a Basic.Deliver (60) frame for the given message and delivery tag.
func createBasicDeliverFrame(channel uint16, consumerTag, exchange, routingKey string, deliveryTag uint64, redelivered bool) ([]byte, error) {
	consumerTagKv := KeyValue{
		Key:   STRING_SHORT,
		Value: consumerTag,
	}
	deliveryTagKv := KeyValue{
		Key:   INT_LONG_LONG,
		Value: deliveryTag,
	}
	redeliveredKv := KeyValue{
		Key:   BIT,
		Value: redelivered,
	}
	exchangeKv := KeyValue{
		Key:   STRING_SHORT,
		Value: exchange,
	}
	routingKeyKv := KeyValue{
		Key:   STRING_SHORT,
		Value: routingKey,
	}
	content := ContentList{
		KeyValuePairs: []KeyValue{consumerTagKv, deliveryTagKv, redeliveredKv, exchangeKv, routingKeyKv},
	}
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_DELIVER),
		Content:  content,
	}.FormatMethodFrame()
}

// createBasicGetOkFrame creates a Basic.GetOk (71) frame.
func createBasicGetOkFrame(channel uint16, exchange, routingkey string, msgCount uint32, deliveryTag uint64, redelivered bool) ([]byte, error) {
	msgGetOk := &BasicGetOkContent{
		DeliveryTag:  deliveryTag,
		Redelivered:  redelivered,
		Exchange:     exchange,
		RoutingKey:   routingkey,
		MessageCount: msgCount,
	}

	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_GET_OK),
		Content:  *EncodeGetOkToContentList(msgGetOk),
	}.FormatMethodFrame()
}

// createBasicGetEmptyFrame creates a Basic.GetEmpty (72) frame.
func createBasicGetEmptyFrame(channel uint16) ([]byte, error) {
	// Send Basic.GetEmpty
	reserved1 := KeyValue{
		Key:   STRING_SHORT,
		Value: "",
	}
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_GET_EMPTY),
		Content:  ContentList{KeyValuePairs: []KeyValue{reserved1}},
	}.FormatMethodFrame()
}

// createBasicRecoverOkFrame creates a Basic.RecoverOk (100) frame.
func createBasicRecoverOkFrame(channel uint16) ([]byte, error) {
	return ResponseMethodMessage{
		Channel:  channel,
		ClassID:  uint16(BASIC),
		MethodID: uint16(BASIC_RECOVER_OK),
		Content:  ContentList{},
	}.FormatMethodFrame()
}

func (props *BasicProperties) encodeBasicProperties() ([]byte, uint16, error) {
	var buf bytes.Buffer
	var flags uint16

	if props.ContentType != "" {
		flags |= (1 << 15)
		if err := EncodeShortStr(&buf, string(props.ContentType)); err != nil {
			return nil, 0, err
		}
	}
	if props.ContentEncoding != "" {
		flags |= (1 << 14)
		if err := EncodeShortStr(&buf, props.ContentEncoding); err != nil {
			return nil, 0, err
		}
	}
	if props.Headers != nil {
		flags |= (1 << 13)
		if err := encodeHeaders(&buf, props.Headers); err != nil {
			return nil, 0, err
		}
	}
	if props.DeliveryMode != 0 {
		flags |= (1 << 12)
		if err := EncodeOctet(&buf, uint8(props.DeliveryMode)); err != nil {
			return nil, 0, err
		}
	}
	if props.Priority != 0 {
		flags |= (1 << 11)
		if err := EncodeOctet(&buf, props.Priority); err != nil {
			return nil, 0, err
		}
	}
	if props.CorrelationID != "" {
		flags |= (1 << 10)
		if err := EncodeShortStr(&buf, props.CorrelationID); err != nil {
			return nil, 0, err
		}
	}
	if props.ReplyTo != "" {
		flags |= (1 << 9)
		if err := EncodeShortStr(&buf, props.ReplyTo); err != nil {
			return nil, 0, err
		}
	}
	if props.Expiration != "" {
		flags |= (1 << 8)
		if err := EncodeShortStr(&buf, props.Expiration); err != nil {
			return nil, 0, err
		}
	}
	if props.MessageID != "" {
		flags |= (1 << 7)
		if err := EncodeShortStr(&buf, props.MessageID); err != nil {
			return nil, 0, err
		}
	}
	if !props.Timestamp.IsZero() {
		flags |= (1 << 6)
		if err := EncodeTimestamp(&buf, props.Timestamp); err != nil {
			return nil, 0, err
		}
	}
	if props.Type != "" {
		flags |= (1 << 5)
		if err := EncodeShortStr(&buf, props.Type); err != nil {
			return nil, 0, err
		}
	}
	if props.UserID != "" {
		flags |= (1 << 4)
		if err := EncodeShortStr(&buf, props.UserID); err != nil {
			return nil, 0, err
		}
	}
	if props.AppID != "" {
		flags |= (1 << 3)
		if err := EncodeShortStr(&buf, props.AppID); err != nil {
			return nil, 0, err
		}
	}
	if props.Reserved != "" {
		flags |= (1 << 2)
		if err := EncodeShortStr(&buf, props.Reserved); err != nil {
			return nil, 0, err
		}
	}
	return buf.Bytes(), flags, nil
}

func decodeBasicHeaderFlags(short uint16) []string {
	flagNames := []string{
		"contentType",
		"contentEncoding",
		"headers",
		"deliveryMode",
		"priority",
		"correlationID",
		"replyTo",
		"expiration",
		"messageID",
		"timestamp",
		"type",
		"userID",
		"appID",
		"reserved",
	}
	var flags []string
	for i := 0; i < len(flagNames); i++ {
		if (short & (1 << uint(15-i))) != 0 {
			flags = append(flags, flagNames[i])
		}
	}
	return flags
}

// ClassID: short
// Weight: short
// Body Size: long long
// Properties flags: short
// Properties: long (table)
func parseBasicHeader(headerPayload []byte) (*HeaderFrame, error) {

	buf := bytes.NewReader(headerPayload)
	classID, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode class ID: %v", err)
	}
	log.Debug().Int("class_id", int(classID)).Msg("Class ID")

	weight, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode weight: %v", err)
	}
	if weight != 0 {
		return nil, fmt.Errorf("weight must be 0")
	}
	log.Trace().Msgf("- HEADER - Weight: %d\n", weight)

	bodySize, err := DecodeLongLongUInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode body size: %v", err)
	}
	log.Trace().Msgf("- HEADER - Body Size: %d\n", bodySize)

	shortFlags, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode flags: %v", err)
	}
	flags := decodeBasicHeaderFlags(shortFlags)
	properties, err := createContentPropertiesTable(flags, buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode properties: %v", err)
	}
	log.Trace().Msgf("- HEADER - properties: %v\n", properties)
	header := &HeaderFrame{
		ClassID:    classID,
		BodySize:   bodySize,
		Properties: properties,
	}
	return header, nil
}

/* REGION Basic Helpers */
func parseBasicMethod(methodID uint16, payload []byte) (any, error) {
	switch methodID {
	case uint16(BASIC_QOS):
		log.Debug().Msg("Received BASIC_QOS frame \n")
		return parseBasicQosFrame(payload)

	case uint16(BASIC_CONSUME):
		log.Debug().Msg("Received BASIC_CONSUME frame \n")
		return parseBasicConsumeFrame(payload)

	case uint16(BASIC_CANCEL):
		log.Debug().Msg("Received BASIC_CANCEL frame \n")
		return parseBasicCancelFrame(payload)

	case uint16(BASIC_CANCEL_OK):
		log.Debug().Msg("Received BASIC_CANCEL_OK frame \n")
		log.Warn().Msg("Server should not receive BASIC_CANCEL_OK frames from clients")
		return nil, fmt.Errorf("server should not receive BASIC_CANCEL_OK frames from clients")

	case uint16(BASIC_PUBLISH):
		log.Debug().Msg("Received BASIC_PUBLISH frame \n")
		return parseBasicPublishFrame(payload)

	case uint16(BASIC_RETURN):
		log.Debug().Msg("Received BASIC_RETURN frame \n")
		return nil, fmt.Errorf(" basic.return not implemented")

	case uint16(BASIC_DELIVER):
		log.Debug().Msg("Received BASIC_DELIVER frame \n")
		log.Warn().Msg("Server should not receive BASIC_DELIVER frames from clients")
		// TODO: return the appropriate exception
		return nil, fmt.Errorf("server should not receive BASIC_DELIVER frames from clients")

	case uint16(BASIC_GET):
		log.Debug().Msg("Received BASIC_GET frame \n")
		return parseBasicGetFrame(payload)

	case uint16(BASIC_ACK):
		log.Debug().Msg("Received BASIC_ACK frame \n")
		return parseBasicAckFrame(payload)

	case uint16(BASIC_REJECT):
		log.Debug().Msg("Received BASIC_REJECT frame \n")
		return parseBasicRejectFrame(payload)

	case uint16(BASIC_RECOVER_ASYNC):
		log.Debug().Msg("Received BASIC_RECOVER_ASYNC frame \n")
		return parseBasicRecoverAsyncFrame(payload)

	case uint16(BASIC_RECOVER):
		log.Debug().Msg("Received BASIC_RECOVER frame \n")
		return parseBasicRecoverFrame(payload)

	case uint16(BASIC_NACK):
		log.Debug().Msg("Received BASIC_NACK frame \n")
		return parseBasicNackFrame(payload)

	default:
		return nil, fmt.Errorf("unknown method ID: %d", methodID)
	}
}

func parseBasicQosFrame(payload []byte) (*RequestMethodMessage, error) {
	// the payload must be at least 7 bytes long
	// 4 (prefetch size) => long = 4 bytes
	// 2 (prefetch count) => short = 2 bytes
	// 1 (flags) => octet = 1 byte
	if len(payload) < 7 {
		return nil, fmt.Errorf("payload too short")
	}
	buf := bytes.NewReader(payload)
	prefetchSize, err := DecodeLongUInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode prefetch size: %v", err)
	}
	prefetchCount, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode prefetch count: %v", err)
	}
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"global"}, true)
	global := flags["global"]
	content := &BasicQosContent{
		PrefetchSize:  prefetchSize,
		PrefetchCount: prefetchCount,
		Global:        global,
	}
	request := &RequestMethodMessage{
		Content: content,
	}
	log.Trace().Interface("content", content).Msgf("basic.qos formatted")
	return request, nil
}

func parseBasicConsumeFrame(payload []byte) (*RequestMethodMessage, error) {
	// the payload must be at least 9 bytes long
	// 2 (reserved1) => short int = 2 bytes
	// 1+ (queue name) => short str = 1 (length) + 0+ bytes
	// 1+ (consumer-tag) => short str = 1 (length) + 0+ bytes
	// 1 (flags) => octet = 1 byte
	// 4+ (arguments - optional) => table = 4 (length) + n bytes) => packed as long str

	// Expected fields:
	// reserved1(shortint),
	// queue (short str),
	// consumer tag (short str),
	// noLocal (bit),
	// noAck (bit),
	// exclusive (bit),
	// nowait (bit)

	if len(payload) < 5 {
		return nil, fmt.Errorf("payload too short")
	}

	buf := bytes.NewReader(payload)
	// reserved1
	_, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode reserved1: %v", err)
	}

	queue, err := DecodeShortStr(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode queue: %v", err)
	}

	consumerTag, err := DecodeShortStr(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode consumer tag: %v", err)
	}

	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"noLocal", "noAck", "exclusive", "nowait"}, true)
	noLocal := flags["noLocal"]
	noAck := flags["noAck"]
	exclusive := flags["exclusive"]
	nowait := flags["nowait"]

	var arguments map[string]any
	if buf.Len() >= 4 {
		argumentsStr, err := DecodeLongStr(buf)
		if err != nil {
			return nil, fmt.Errorf("failed to decode arguments: %v", err)
		}
		if len(argumentsStr) > 0 {
			arguments, err = DecodeTable([]byte(argumentsStr))
			if err != nil {
				return nil, fmt.Errorf("failed to read arguments: %v", err)
			}
		} else {
			// Empty table - create empty map to distinguish from nil
			arguments = make(map[string]any)
		}
	}
	// If buf.Len() < 4, no arguments table is present, keep arguments as nil

	content := &BasicConsumeContent{
		Queue:       queue,
		ConsumerTag: consumerTag,
		NoLocal:     noLocal,
		NoAck:       noAck,
		Exclusive:   exclusive,
		NoWait:      nowait,
		Arguments:   arguments,
	}
	request := &RequestMethodMessage{
		Content: content,
	}
	log.Debug().Interface("BasicConsume", content).Msg("BasicConsume formatted")
	return request, nil
}

func parseBasicCancelFrame(payload []byte) (*RequestMethodMessage, error) {
	// the payload must be at least 2 bytes long
	// 1+ (consumer-tag) => short str = 1 (length) + 1+ bytes -- i don't accept empty consumer tags
	// 1 (flags) => octet = 1 byte
	if len(payload) < 2 {
		return nil, fmt.Errorf("payload too short")
	}

	buf := bytes.NewReader(payload)
	consumerTag, err := DecodeShortStr(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode consumer tag: %v", err)
	}
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"nowait"}, true)
	nowait := flags["nowait"]

	content := &BasicCancelContent{
		ConsumerTag: consumerTag,
		NoWait:      nowait,
	}
	request := &RequestMethodMessage{
		Content: content,
	}
	return request, nil
}

func parseBasicPublishFrame(payload []byte) (*RequestMethodMessage, error) {
	// the payload must be at least 5 bytes long
	// 2 (reserved1) => 2 bytes
	// 1+ (exchange name) => short str = 1  (length) + 1 byte
	// 1+ (routing key) => short str = 1 (length) + 1 byte
	// 1 (flags) => octet = 1 byte
	if len(payload) < 5 {
		return nil, fmt.Errorf("payload too short")
	}

	buf := bytes.NewReader(payload)

	// Note: AMQP spec says "shortstr" but all real implementations
	// (RabbitMQ, major clients) use short int (2 bytes) for reserved fields.
	// We follow industry practice for compatibility.
	_, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode reserved1: %v", err)
	}
	exchange, err := DecodeShortStr(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exchange: %v", err)
	}
	routingKey, err := DecodeShortStr(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode routing key: %v", err)
	}
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"mandatory", "immediate"}, true)
	mandatory := flags["mandatory"]
	immediate := flags["immediate"]
	msg := &BasicPublishContent{
		Exchange:   exchange,
		RoutingKey: routingKey,
		Mandatory:  mandatory,
		Immediate:  immediate,
	}
	request := &RequestMethodMessage{
		Content: msg,
	}
	log.Trace().Interface("msg", msg).Msg("basic.publish formatted")
	return request, nil
}

func parseBasicGetFrame(payload []byte) (*RequestMethodMessage, error) {
	buf := bytes.NewReader(payload)
	reserved1, err := DecodeShortInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode reserved1: %v", err)
	}
	if reserved1 != 0 {
		return nil, fmt.Errorf("reserved1 must be 0")
	}
	queue, err := DecodeShortStr(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exchange: %v", err)
	}
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"noAck"}, true)
	noAck := flags["noAck"]
	msg := &BasicGetMessageContent{
		Reserved1: reserved1,
		Queue:     queue,
		NoAck:     noAck,
	}
	return &RequestMethodMessage{
		Content: msg,
	}, nil
}

func parseBasicAckFrame(payload []byte) (*RequestMethodMessage, error) {
	// Expected fields:
	// 8 deliveryTag (long long int),
	// 1 multiple (bit - packed as octet)
	if len(payload) < 9 {
		return nil, fmt.Errorf("payload too short: payload length %d", len(payload))
	}
	buf := bytes.NewReader(payload)
	deliveryTag, err := DecodeLongLongUInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode deliveryTag: %v", err)
	}
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"multiple"}, true)
	multiple := flags["multiple"]
	content := &BasicAckContent{
		DeliveryTag: deliveryTag,
		Multiple:    multiple,
	}
	return &RequestMethodMessage{
		Content: content,
	}, nil

}

func parseBasicRejectFrame(payload []byte) (*RequestMethodMessage, error) {
	// Expected fields:
	// 8 deliveryTag (long long int),
	// 1 requeue (bit - packed as octet)
	if len(payload) < 9 {
		return nil, fmt.Errorf("payload too short: payload length %d", len(payload))
	}
	buf := bytes.NewReader(payload)
	deliveryTag, err := DecodeLongLongUInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode deliveryTag: %v", err)
	}
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"requeue"}, true)
	requeue := flags["requeue"]
	content := &BasicRejectContent{
		DeliveryTag: deliveryTag,
		Requeue:     requeue,
	}
	return &RequestMethodMessage{
		Content: content,
	}, nil
}

func parseBasicRecoverAsyncFrame(payload []byte) (*RequestMethodMessage, error) {
	request, err := parseBasicRecoverFrame(payload)
	if err != nil {
		return nil, err
	}
	if content, ok := request.Content.(*BasicRecoverContent); ok && content != nil {
		content.Async = true
	}
	return request, nil
}

func parseBasicRecoverFrame(payload []byte) (*RequestMethodMessage, error) {
	// Expected fields:
	// 1 requeue (bit - packed as octet)
	if len(payload) < 9 {
		return nil, fmt.Errorf("payload too short: payload length %d", len(payload))
	}
	buf := bytes.NewReader(payload)

	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"requeue"}, true)
	requeue := flags["requeue"]
	content := &BasicRecoverContent{
		Requeue: requeue,
		Async:   false,
	}
	return &RequestMethodMessage{
		Content: content,
	}, nil
}

func parseBasicNackFrame(payload []byte) (*RequestMethodMessage, error) {
	// Expected fields:
	// 8 deliveryTag (long long int),
	// flags:  (bit - packed as octet)
	// multiple + requeue
	if len(payload) < 9 {
		return nil, fmt.Errorf("payload too short: payload length %d", len(payload))
	}
	buf := bytes.NewReader(payload)
	deliveryTag, err := DecodeLongLongUInt(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to decode deliveryTag: %v", err)
	}
	octet, err := buf.ReadByte()
	if err != nil {
		return nil, fmt.Errorf("failed to read octet: %v", err)
	}
	flags := DecodeFlags(octet, []string{"multiple", "requeue"}, true)
	multiple := flags["multiple"]
	requeue := flags["requeue"]
	content := &BasicNackContent{
		DeliveryTag: deliveryTag,
		Multiple:    multiple,
		Requeue:     requeue,
	}
	return &RequestMethodMessage{
		Content: content,
	}, nil
}
