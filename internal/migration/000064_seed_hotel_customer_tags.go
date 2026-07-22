package migration

import (
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

type standardHotelTagSeed struct {
	Name            string
	SemanticKey     string
	Aliases         string
	ConflictGroup   string
	ApplicableScene string
}

type standardHotelTagCategorySeed struct {
	Name     string
	SortNo   int
	Children []standardHotelTagSeed
}

func init() {
	register(64, "seed standard hotel customer tags", func() error {
		return seedStandardHotelCustomerTags(sqls.DB())
	})
}

func seedStandardHotelCustomerTags(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	now := time.Now()
	return db.Transaction(func(tx *gorm.DB) error {
		for _, category := range standardHotelTagCategories() {
			parent, err := upsertStandardHotelTag(tx, &models.Tag{
				CompanyID: 0, ParentID: 0, Name: category.Name,
				SemanticKey: "category.hotel", AIEnabled: false, ReplyEnabled: false,
				ApplicableScene: "customer_profile", SortNo: category.SortNo, Status: enums.StatusOk,
				AuditFields: systemTagAuditFields(now),
			})
			if err != nil {
				return err
			}
			for index, child := range category.Children {
				if _, err := upsertStandardHotelTag(tx, &models.Tag{
					CompanyID: 0, ParentID: parent.ID, Name: child.Name,
					SemanticKey: child.SemanticKey, Aliases: child.Aliases,
					ConflictGroup: child.ConflictGroup, AIEnabled: true, ReplyEnabled: false,
					ApplicableScene: child.ApplicableScene, SortNo: index + 1, Status: enums.StatusOk,
					AuditFields: systemTagAuditFields(now),
				}); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func upsertStandardHotelTag(db *gorm.DB, seed *models.Tag) (*models.Tag, error) {
	current := &models.Tag{}
	err := db.Take(current, "company_id = ? AND parent_id = ? AND name = ?", seed.CompanyID, seed.ParentID, seed.Name).Error
	if err == nil {
		if err := db.Model(current).Updates(map[string]any{
			"semantic_key": seed.SemanticKey, "aliases": seed.Aliases,
			"conflict_group": seed.ConflictGroup, "ai_enabled": seed.AIEnabled,
			"reply_enabled": seed.ReplyEnabled, "applicable_scene": seed.ApplicableScene,
			"merged_into_tag_id": 0, "sort_no": seed.SortNo, "status": seed.Status,
			"update_user_id":   constants.SystemAuditUserID,
			"update_user_name": constants.SystemAuditUserName, "updated_at": seed.UpdatedAt,
		}).Error; err != nil {
			return nil, err
		}
		return current, nil
	}
	if err != gorm.ErrRecordNotFound {
		return nil, err
	}
	if err := db.Create(seed).Error; err != nil {
		return nil, err
	}
	return seed, nil
}

func systemTagAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, UpdatedAt: now,
		CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
		UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
	}
}

func standardHotelTagCategories() []standardHotelTagCategorySeed {
	return []standardHotelTagCategorySeed{
		{Name: "房间偏好", SortNo: 1, Children: []standardHotelTagSeed{
			{Name: "喜静", SemanticKey: "room.quiet", Aliases: "安静,怕吵,睡眠浅", ConflictGroup: "room.noise", ApplicableScene: "room_assignment"},
			{Name: "无烟", SemanticKey: "room.non_smoking", Aliases: "禁烟,不吸烟,无烟房", ConflictGroup: "room.smoking", ApplicableScene: "room_assignment"},
			{Name: "可吸烟", SemanticKey: "room.smoking", Aliases: "吸烟,烟房", ConflictGroup: "room.smoking", ApplicableScene: "room_assignment"},
			{Name: "大床", SemanticKey: "room.king_bed", Aliases: "大床房,一张床", ConflictGroup: "room.bed", ApplicableScene: "room_assignment"},
			{Name: "双床", SemanticKey: "room.twin_bed", Aliases: "双床房,两张床", ConflictGroup: "room.bed", ApplicableScene: "room_assignment"},
			{Name: "高楼层", SemanticKey: "room.high_floor", Aliases: "高层,楼层高", ConflictGroup: "room.floor", ApplicableScene: "room_assignment"},
			{Name: "低楼层", SemanticKey: "room.low_floor", Aliases: "低层,楼层低", ConflictGroup: "room.floor", ApplicableScene: "room_assignment"},
			{Name: "近电梯", SemanticKey: "room.near_elevator", Aliases: "靠电梯,离电梯近", ConflictGroup: "room.elevator", ApplicableScene: "room_assignment"},
			{Name: "远电梯", SemanticKey: "room.far_elevator", Aliases: "远离电梯,离电梯远", ConflictGroup: "room.elevator", ApplicableScene: "room_assignment"},
			{Name: "要窗", SemanticKey: "room.window", Aliases: "有窗,需要窗户,外窗", ApplicableScene: "room_assignment"},
			{Name: "亲子房", SemanticKey: "room.family", Aliases: "家庭房,儿童房", ApplicableScene: "room_selection"},
			{Name: "宠物房", SemanticKey: "room.pet_friendly", Aliases: "宠物友好,带宠房", ApplicableScene: "room_selection"},
		}},
		{Name: "位置偏好", SortNo: 2, Children: []standardHotelTagSeed{
			{Name: "近地铁", SemanticKey: "location.metro", Aliases: "地铁附近,靠地铁", ConflictGroup: "location.preference", ApplicableScene: "hotel_selection"},
			{Name: "近车站", SemanticKey: "location.station", Aliases: "车站附近,靠车站", ConflictGroup: "location.preference", ApplicableScene: "hotel_selection"},
			{Name: "近景点", SemanticKey: "location.attraction", Aliases: "景点附近,靠景区", ConflictGroup: "location.preference", ApplicableScene: "hotel_selection"},
			{Name: "近商圈", SemanticKey: "location.business", Aliases: "商圈附近,购物方便", ConflictGroup: "location.preference", ApplicableScene: "hotel_selection"},
			{Name: "近医院", SemanticKey: "location.hospital", Aliases: "医院附近,就医方便", ConflictGroup: "location.preference", ApplicableScene: "hotel_selection"},
		}},
		{Name: "入住习惯", SortNo: 3, Children: []standardHotelTagSeed{
			{Name: "晚到", SemanticKey: "stay.late_arrival", Aliases: "晚入住,深夜到店", ConflictGroup: "stay.arrival", ApplicableScene: "arrival_service"},
			{Name: "早到", SemanticKey: "stay.early_arrival", Aliases: "早入住,提前到店", ConflictGroup: "stay.arrival", ApplicableScene: "arrival_service"},
			{Name: "常住", SemanticKey: "stay.frequent", Aliases: "经常住,常客", ApplicableScene: "customer_profile"},
			{Name: "连住", SemanticKey: "stay.extended", Aliases: "连续入住,多晚入住", ApplicableScene: "stay_service"},
			{Name: "晚退房", SemanticKey: "stay.late_checkout", Aliases: "延迟退房,晚点退房", ConflictGroup: "stay.checkout", ApplicableScene: "checkout_service"},
			{Name: "早退房", SemanticKey: "stay.early_checkout", Aliases: "提前退房,一早退房", ConflictGroup: "stay.checkout", ApplicableScene: "checkout_service"},
			{Name: "要发票", SemanticKey: "stay.invoice", Aliases: "开发票,需要发票", ApplicableScene: "invoice_service"},
		}},
		{Name: "出行属性", SortNo: 4, Children: []standardHotelTagSeed{
			{Name: "商务", SemanticKey: "travel.business", Aliases: "出差,商务出行", ConflictGroup: "travel.type", ApplicableScene: "customer_profile"},
			{Name: "亲子", SemanticKey: "travel.family", Aliases: "带孩子,家庭出行", ConflictGroup: "travel.type", ApplicableScene: "customer_profile"},
			{Name: "情侣", SemanticKey: "travel.couple", Aliases: "情侣出行,两人约会", ConflictGroup: "travel.type", ApplicableScene: "customer_profile"},
			{Name: "自驾", SemanticKey: "travel.driving", Aliases: "开车,需要停车", ApplicableScene: "parking_service"},
			{Name: "带宠", SemanticKey: "travel.pet", Aliases: "带宠物,宠物同行", ApplicableScene: "pet_service"},
			{Name: "独行", SemanticKey: "travel.solo", Aliases: "一个人,单独出行", ConflictGroup: "travel.party", ApplicableScene: "customer_profile"},
			{Name: "团体", SemanticKey: "travel.group", Aliases: "多人同行,团队出行", ConflictGroup: "travel.party", ApplicableScene: "customer_profile"},
		}},
		{Name: "服务偏好", SortNo: 5, Children: []standardHotelTagSeed{
			{Name: "硬枕", SemanticKey: "service.hard_pillow", Aliases: "硬一点的枕头,硬枕头", ConflictGroup: "service.pillow", ApplicableScene: "room_service"},
			{Name: "软枕", SemanticKey: "service.soft_pillow", Aliases: "软一点的枕头,软枕头", ConflictGroup: "service.pillow", ApplicableScene: "room_service"},
			{Name: "多要水", SemanticKey: "service.extra_water", Aliases: "多放水,多送水", ApplicableScene: "room_service"},
			{Name: "少打扰", SemanticKey: "service.do_not_disturb", Aliases: "不要打扰,减少打扰", ApplicableScene: "room_service"},
			{Name: "要清洁", SemanticKey: "service.daily_cleaning", Aliases: "每天打扫,需要清洁", ApplicableScene: "room_service"},
			{Name: "需接送", SemanticKey: "service.transfer", Aliases: "需要接送,接机送站", ApplicableScene: "transport_service"},
		}},
	}
}
