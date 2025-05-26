package torrent

type extensionId int

const (
	utMetadataId extensionId = iota + 1
)

type extendedMessageType int

const (
	extMsgRequest extendedMessageType = iota
	extMsgData
	extMsgReject
)

type extensionName string

const (
	utMetadata extensionName = "ut_metadata"
)
