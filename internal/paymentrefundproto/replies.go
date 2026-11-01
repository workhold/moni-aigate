package paymentrefundproto

var commandReplyFields = []string{"schema_version", "command", "parameter_digest", "command_state", "result"}
var queryReplyFields = []string{"schema_version", "query_id", "mutation_generation", "command", "parameter_digest", "command_state", "result"}

func resultValid(command RefundRequest, state string, result *RefundFact, query bool) bool {
	switch state {
	case "not_seen":
		return query && result == nil
	case "in_progress":
		return result == nil
	case "completed":
		return result != nil && factValid(*result) && commandOf(*result) == command
	}
	return false
}
func commandReplyValid(r CommandReply) bool {
	d, err := ParameterDigest(r.Command)
	return r.SchemaVersion == 2 && err == nil && r.ParameterDigest == d && resultValid(r.Command, r.CommandState, r.Result, false)
}
func queryReplyValid(r QueryReply) bool {
	return queryValid(QueryRequest{r.SchemaVersion, r.QueryID, r.MutationGeneration, r.Command, r.ParameterDigest}) && resultValid(r.Command, r.CommandState, r.Result, true)
}
func CanonicalCommandReply(r CommandReply) ([]byte, error) {
	if !commandReplyValid(r) {
		return nil, ErrInvalid
	}
	return encode(r, bodyLimit)
}
func CanonicalQueryReply(r QueryReply) ([]byte, error) {
	if !queryReplyValid(r) {
		return nil, ErrInvalid
	}
	return encode(r, bodyLimit)
}
func ParseCommandReply(raw []byte) (CommandReply, error) {
	var r CommandReply
	m, err := strictObject(raw, bodyLimit, commandReplyFields, []string{"result"}, &r)
	if err != nil {
		return CommandReply{}, ErrInvalid
	}
	r.Command, err = ParseRefundRequest(m["command"])
	if err != nil {
		return CommandReply{}, ErrInvalid
	}
	if !null(m["result"]) {
		f, err := ParseRefundFact(m["result"])
		if err != nil {
			return CommandReply{}, ErrInvalid
		}
		r.Result = &f
	}
	if !commandReplyValid(r) {
		return CommandReply{}, ErrInvalid
	}
	return r, nil
}
func ParseQueryReply(raw []byte) (QueryReply, error) {
	var r QueryReply
	m, err := strictObject(raw, bodyLimit, queryReplyFields, []string{"result"}, &r)
	if err != nil {
		return QueryReply{}, ErrInvalid
	}
	r.Command, err = ParseRefundRequest(m["command"])
	if err != nil {
		return QueryReply{}, ErrInvalid
	}
	if !null(m["result"]) {
		f, err := ParseRefundFact(m["result"])
		if err != nil {
			return QueryReply{}, ErrInvalid
		}
		r.Result = &f
	}
	if !queryReplyValid(r) {
		return QueryReply{}, ErrInvalid
	}
	return r, nil
}
