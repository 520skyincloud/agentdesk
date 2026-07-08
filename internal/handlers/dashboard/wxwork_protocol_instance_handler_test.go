package dashboard

import (
	"testing"

	"agent-desk/internal/pkg/dto/response"
)

func TestParseWxWorkProtocolRoomOptionsSupportsOfficialRoomShapes(t *testing.T) {
	raw := `{
		"code":0,
		"data":{
			"chatrooms":[
				{"chat_id":"abc123","room_name":"门店接待群","owner_id":"S:owner","member_cnt":5},
				{"conversation_id":"R:def456","name":"夜班群","member_count":"3"}
			]
		}
	}`
	rooms := parseWxWorkProtocolRoomOptions(raw)
	if len(rooms) != 2 {
		t.Fatalf("expected 2 rooms, got %d: %#v", len(rooms), rooms)
	}
	if rooms[0].RoomID != "abc123" || rooms[0].ConversationID != "R:abc123" || rooms[0].Name != "门店接待群" || rooms[0].MemberCount != 5 {
		t.Fatalf("unexpected first room: %#v", rooms[0])
	}
	if rooms[1].RoomID != "def456" || rooms[1].ConversationID != "R:def456" || rooms[1].MemberCount != 3 {
		t.Fatalf("unexpected second room: %#v", rooms[1])
	}
}

func TestNormalizeWxWorkProtocolRoomID(t *testing.T) {
	if got := normalizeWxWorkProtocolRoomID(" R:room-1 "); got != "room-1" {
		t.Fatalf("expected room-1, got %q", got)
	}
	if got := normalizeWxWorkProtocolRoomID("r:room-2"); got != "room-2" {
		t.Fatalf("expected room-2, got %q", got)
	}
}

func TestParseWxWorkProtocolRoomOptionsSupportsGetRoomListRoomdata(t *testing.T) {
	raw := `{"error_code":0,"error_message":"ok","data":{"roomdata":{"datas":[{"id":"7659295984471507546","roomid":"10763697899844953","owner_vid":"1688854374868018","member_count":1,"roomname":""},{"id":"7656740749513326593","roomid":"10775325120961882","owner_vid":"1688854374868018","member_count":2,"roomname":"门店群"}]},"next_start":-1,"total":2}}`
	rooms := parseWxWorkProtocolRoomOptions(raw)
	if len(rooms) != 2 {
		t.Fatalf("expected 2 rooms, got %d: %#v", len(rooms), rooms)
	}
	if rooms[0].RoomID != "10763697899844953" || rooms[0].ConversationID != "R:10763697899844953" || rooms[0].Owner != "1688854374868018" || rooms[0].MemberCount != 1 {
		t.Fatalf("unexpected first room: %#v", rooms[0])
	}
	if rooms[0].Name == "" {
		t.Fatalf("expected fallback name for unnamed room")
	}
	if rooms[1].Name != "门店群" || rooms[1].MemberCount != 2 {
		t.Fatalf("unexpected second room: %#v", rooms[1])
	}
}

func TestMergeWxWorkProtocolRoomDetailsUsesRoomRemark(t *testing.T) {
	rooms := []response.WxWorkProtocolRoomOptionResponse{{RoomID: "10763697899844953", ConversationID: "R:10763697899844953", Name: "群聊 10763697899844953（未命名）", MemberCount: 1}}
	detailRaw := `{"error_code":0,"data":{"roominfos":[{"info":{"roomid":"10763697899844953","createuin":"1688854374868018","roomname":""},"members":[{"uin":"1688854374868018","roomname_remark":"知悉测试"}]}]}}`
	merged := mergeWxWorkProtocolRoomDetails(rooms, detailRaw)
	if len(merged) != 1 {
		t.Fatalf("expected 1 room, got %#v", merged)
	}
	if merged[0].Name != "知悉测试" || merged[0].Owner != "1688854374868018" {
		t.Fatalf("expected detail name/owner merged, got %#v", merged[0])
	}
}

func TestExtractWxWorkProtocolRoomMemberIDsFromRoomDetail(t *testing.T) {
	raw := `{"data":{"room_list":[{"room_id":"abc","member_list":[{"user_id":"S:one","name":"A"},{"userid":"S:two","name":"B"}],"memberIds":[{"userId":"S:three"}]}]}}`
	ids := extractWxWorkProtocolRoomMemberIDs(raw)
	if len(ids) != 3 || ids[0] != "S:one" || ids[1] != "S:two" || ids[2] != "S:three" {
		t.Fatalf("unexpected member ids: %#v", ids)
	}
}

func TestParseWxWorkProtocolRoomMemberOptionsFromRoomDetailMembers(t *testing.T) {
	raw := `{"error_code":0,"data":{"roominfos":[{"info":{"roomid":"10763697899844953"},"members":[{"uin":"1688854374868018","roomname_remark":"知悉测试"},{"uin":"7881302995969629","nickname":"来一杯生椰拿铁"}]}]}}`
	members := parseWxWorkProtocolRoomMemberOptions(raw)
	if len(members) != 2 {
		t.Fatalf("expected 2 members, got %#v", members)
	}
	if members[0].UserID != "1688854374868018" || members[0].Name != "知悉测试" {
		t.Fatalf("unexpected first member: %#v", members[0])
	}
	if members[0].RoomRemark != "知悉测试" {
		t.Fatalf("unexpected first member room remark: %#v", members[0])
	}
	if members[1].UserID != "7881302995969629" || members[1].Name != "来一杯生椰拿铁" {
		t.Fatalf("unexpected second member: %#v", members[1])
	}
}

func TestParseWxWorkProtocolRoomMemberOptionsFromMemberDetailPersons(t *testing.T) {
	raw := `{"error_code":0,"data":{"persons":[{"vid":"1688854374868018","info":{"uin":"1688854374868018","name":"吴朝伟","realname":"吴朝伟","acctid":"WuChaoWei","iconurl":"https://example.com/avatar.png"}}]}}`
	members := parseWxWorkProtocolRoomMemberOptions(raw)
	if len(members) != 1 {
		t.Fatalf("expected 1 member, got %#v", members)
	}
	if members[0].UserID != "1688854374868018" || members[0].Name != "吴朝伟" || members[0].RealName != "吴朝伟" || members[0].AccountID != "WuChaoWei" || members[0].Avatar == "" {
		t.Fatalf("unexpected member: %#v", members[0])
	}
}

func TestMergeWxWorkProtocolRoomMemberOptionPrefersRealName(t *testing.T) {
	current := response.WxWorkProtocolRoomMemberOptionResponse{
		UserID:     "1688854374868018",
		Name:       "知悉测试",
		RoomRemark: "知悉测试",
	}
	next := response.WxWorkProtocolRoomMemberOptionResponse{
		UserID:    "1688854374868018",
		Name:      "吴朝伟",
		RealName:  "吴朝伟",
		AccountID: "WuChaoWei",
	}
	merged := mergeWxWorkProtocolRoomMemberOption(current, next)
	if merged.Name != "吴朝伟" || merged.RealName != "吴朝伟" || merged.RoomRemark != "知悉测试" || merged.AccountID != "WuChaoWei" {
		t.Fatalf("unexpected merged member: %#v", merged)
	}
}
