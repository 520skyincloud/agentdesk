package migration

import (
	"fmt"
	"strings"
	"time"

	"agent-desk/internal/models"
	"agent-desk/internal/pkg/constants"
	"agent-desk/internal/pkg/enums"
	"agent-desk/internal/pkg/replyintent"
	"agent-desk/internal/repositories"
	"agent-desk/internal/services"

	"github.com/mlogclub/simple/sqls"
	"gorm.io/gorm"
)

const seedIndustryCatalogMigrationRemark = "seed authoritative industry intent and tag catalog"

type unifiedHotelTagSeed struct {
	Name            string
	SemanticKey     string
	Aliases         string
	ConflictGroup   string
	ApplicableScene string
	ReplyEnabled    bool
}

type unifiedHotelTagCategorySeed struct {
	Name        string
	SemanticKey string
	SortNo      int
	Children    []unifiedHotelTagSeed
}

func init() {
	register(68, seedIndustryCatalogMigrationRemark, func() error {
		return seedIndustryCatalog(sqls.DB())
	})
}

func seedIndustryCatalog(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("industry catalog seed database is nil")
	}
	return db.Transaction(func(tx *gorm.DB) error {
		profile, err := upsertAuthoritativeHotelProfile(tx)
		if err != nil {
			return err
		}
		if err := upsertAuthoritativeHotelIntents(tx, profile.ID); err != nil {
			return err
		}
		if err := upsertAuthoritativeHotelTagDefinitions(tx, profile); err != nil {
			return err
		}

		fallback := repositories.TenantRepository.GetByTenantCode(tx, constants.LegacyDefaultTenantCode)
		if fallback == nil {
			return fmt.Errorf("OIDC fallback tenant is missing before industry catalog seed")
		}
		if err := services.TenantService.InitializeSystemTenantDB(tx, fallback, profile.ID); err != nil {
			return fmt.Errorf("initialize OIDC fallback tenant industry: %w", err)
		}
		return nil
	})
}

func upsertAuthoritativeHotelProfile(db *gorm.DB) (*models.ReplyIntentProfile, error) {
	now := time.Now()
	profile := repositories.ReplyIntentProfileRepository.Take(db, "code = ?", replyintent.DefaultHotelProfileCode)
	if profile == nil {
		profile = &models.ReplyIntentProfile{
			Code:               replyintent.DefaultHotelProfileCode,
			Name:               "酒店行业",
			IndustryCode:       replyintent.DefaultHotelIndustryCode,
			Description:        "无人化酒店客服回复链路的行业意图、知识和客户标签事实源。",
			IntentDetectPrompt: replyintent.DefaultHotelIntentDetectPrompt(),
			IntentJSONSchema:   replyintent.DefaultHotelIntentJSONSchema(),
			Revision:           1,
			PublishedAt:        &now,
			PublishedBy:        constants.SystemAuditUserID,
			Status:             enums.StatusOk,
			SortNo:             10,
			Remark:             "统一集成：酒店行业权威 Profile",
			AuditFields:        unifiedIndustryAuditFields(now),
		}
		if err := repositories.ReplyIntentProfileRepository.Create(db, profile); err != nil {
			return nil, err
		}
		return profile, nil
	}

	revision := profile.Revision
	if revision <= 0 {
		revision = 1
	}
	definitionChanged := strings.TrimSpace(profile.IndustryCode) != replyintent.DefaultHotelIndustryCode ||
		profile.IntentDetectPrompt != replyintent.DefaultHotelIntentDetectPrompt() ||
		profile.IntentJSONSchema != replyintent.DefaultHotelIntentJSONSchema()
	if definitionChanged && profile.Revision > 0 {
		revision++
	}
	updates := map[string]any{
		"name":                 "酒店行业",
		"industry_code":        replyintent.DefaultHotelIndustryCode,
		"description":          "无人化酒店客服回复链路的行业意图、知识和客户标签事实源。",
		"intent_detect_prompt": replyintent.DefaultHotelIntentDetectPrompt(),
		"intent_json_schema":   replyintent.DefaultHotelIntentJSONSchema(),
		"revision":             revision,
		"published_by":         constants.SystemAuditUserID,
		"status":               enums.StatusOk,
		"sort_no":              10,
		"remark":               "统一集成：酒店行业权威 Profile",
		"update_user_id":       constants.SystemAuditUserID,
		"update_user_name":     constants.SystemAuditUserName,
		"updated_at":           now,
	}
	if profile.PublishedAt == nil || definitionChanged {
		updates["published_at"] = now
	}
	if err := repositories.ReplyIntentProfileRepository.Updates(db, profile.ID, updates); err != nil {
		return nil, err
	}
	profile = repositories.ReplyIntentProfileRepository.Get(db, profile.ID)
	if profile == nil {
		return nil, fmt.Errorf("reload authoritative hotel industry profile")
	}
	return profile, nil
}

func upsertAuthoritativeHotelIntents(db *gorm.DB, profileID int64) error {
	now := time.Now()
	for sortNo, seed := range defaultReplyIntentConfigs() {
		current := repositories.ReplyIntentConfigRepository.Take(db,
			"code = ? AND intent_profile_id = ?",
			seed.Code, profileID,
		)
		columns := unifiedIntentColumns(seed, profileID, sortNo+1, now)
		if current == nil {
			item := &models.ReplyIntentConfig{
				Code: seed.Code, Name: seed.Name, IntentProfileID: profileID, Status: enums.StatusOk,
				AuditFields: unifiedIndustryAuditFields(now),
			}
			if err := repositories.ReplyIntentConfigRepository.Create(db, item); err != nil {
				return err
			}
			current = item
		}
		if err := repositories.ReplyIntentConfigRepository.Updates(db, current.ID, columns); err != nil {
			return err
		}
	}
	return nil
}

func unifiedIntentColumns(seed defaultReplyIntentConfig, profileID int64, sortNo int, now time.Time) map[string]any {
	return map[string]any{
		"code": seed.Code, "name": seed.Name, "description": seed.Name,
		"intent_profile_id": profileID, "priority": seed.Priority,
		"match_mode": "hybrid", "keywords": seed.Keywords, "positive_examples": seed.PositiveExamples,
		"required_context": seed.RequiredContext, "needs_knowledge": seed.NeedsKnowledge,
		"needs_resource": seed.NeedsResource, "resource_type": seed.ResourceType,
		"needs_human_route": seed.NeedsHumanRoute, "human_route_policy": seed.HumanRoutePolicy,
		"prompt_pack": seed.PromptPack, "reply_plan_template": seed.ReplyPlan,
		"validation_rules": seed.ValidationRules, "no_reply_when_matched": seed.NoReply,
		"status": enums.StatusOk, "sort_no": sortNo,
		"remark":         "统一集成：行业 Profile 固定意图分类",
		"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		"updated_at": now,
	}
}

func upsertAuthoritativeHotelTagDefinitions(db *gorm.DB, profile *models.ReplyIntentProfile) error {
	if profile == nil || profile.ID <= 0 {
		return fmt.Errorf("hotel industry profile is missing")
	}
	now := time.Now()
	for _, category := range unifiedHotelTagCategories() {
		parent, err := upsertIndustryTagDefinition(db, profile, 0, category.Name, category.SemanticKey, "", "", "", false, category.SortNo, now)
		if err != nil {
			return err
		}
		for index, child := range category.Children {
			_, err := upsertIndustryTagDefinition(
				db, profile, parent.ID, child.Name, child.SemanticKey, child.Aliases,
				child.ConflictGroup, child.ApplicableScene, child.ReplyEnabled, index+1, now,
			)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func upsertIndustryTagDefinition(
	db *gorm.DB,
	profile *models.ReplyIntentProfile,
	parentID int64,
	name, semanticKey, aliases, conflictGroup, applicableScene string,
	replyEnabled bool,
	sortNo int,
	now time.Time,
) (*models.IndustryTagDefinition, error) {
	current := repositories.IndustryTagDefinitionRepository.TakeBySemanticKey(db, profile.ID, semanticKey)
	columns := map[string]any{
		"parent_id": parentID, "name": name, "semantic_key": semanticKey, "aliases": aliases,
		"conflict_group": conflictGroup, "applicable_scene": applicableScene,
		"ai_enabled": parentID > 0, "reply_enabled": replyEnabled,
		"definition_revision": profile.Revision, "sort_no": sortNo, "status": enums.StatusOk,
		"update_user_id": constants.SystemAuditUserID, "update_user_name": constants.SystemAuditUserName,
		"updated_at": now,
	}
	if current == nil {
		current = &models.IndustryTagDefinition{
			IntentProfileID: profile.ID, ParentID: parentID, Name: name, SemanticKey: semanticKey,
			Aliases: aliases, ConflictGroup: conflictGroup, ApplicableScene: applicableScene,
			AIEnabled: parentID > 0, ReplyEnabled: replyEnabled,
			DefinitionRevision: profile.Revision, SortNo: sortNo, Status: enums.StatusOk,
			AuditFields: unifiedIndustryAuditFields(now),
		}
		if err := repositories.IndustryTagDefinitionRepository.Create(db, current); err != nil {
			return nil, err
		}
		return current, nil
	}
	if err := repositories.IndustryTagDefinitionRepository.Updates(db, current.ID, columns); err != nil {
		return nil, err
	}
	return repositories.IndustryTagDefinitionRepository.TakeBySemanticKey(db, profile.ID, semanticKey), nil
}

func unifiedIndustryAuditFields(now time.Time) models.AuditFields {
	return models.AuditFields{
		CreatedAt: now, UpdatedAt: now,
		CreateUserID: constants.SystemAuditUserID, CreateUserName: constants.SystemAuditUserName,
		UpdateUserID: constants.SystemAuditUserID, UpdateUserName: constants.SystemAuditUserName,
	}
}

func unifiedHotelTagCategories() []unifiedHotelTagCategorySeed {
	return []unifiedHotelTagCategorySeed{
		{Name: "房间偏好", SemanticKey: "category.room_preference", SortNo: 1, Children: []unifiedHotelTagSeed{
			{Name: "喜静", SemanticKey: "room.quiet", Aliases: "安静,怕吵,睡眠浅", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "无烟", SemanticKey: "room.non_smoking", Aliases: "禁烟,不吸烟,无烟房", ConflictGroup: "room.smoking", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "可吸烟", SemanticKey: "room.smoking", Aliases: "吸烟,烟房", ConflictGroup: "room.smoking", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "大床", SemanticKey: "room.king_bed", Aliases: "大床房,一张床", ConflictGroup: "room.bed", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "双床", SemanticKey: "room.twin_bed", Aliases: "双床房,两张床", ConflictGroup: "room.bed", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "高楼层", SemanticKey: "room.high_floor", Aliases: "高层,楼层高", ConflictGroup: "room.floor", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "低楼层", SemanticKey: "room.low_floor", Aliases: "低层,楼层低", ConflictGroup: "room.floor", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "近电梯", SemanticKey: "room.near_elevator", Aliases: "靠电梯,离电梯近", ConflictGroup: "room.elevator", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "远电梯", SemanticKey: "room.far_elevator", Aliases: "远离电梯,离电梯远", ConflictGroup: "room.elevator", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "要窗", SemanticKey: "room.window", Aliases: "有窗,需要窗户,外窗", ApplicableScene: "room_assignment", ReplyEnabled: true},
			{Name: "亲子房", SemanticKey: "room.family", Aliases: "家庭房,儿童房", ApplicableScene: "room_selection", ReplyEnabled: true},
			{Name: "宠物房", SemanticKey: "room.pet_friendly", Aliases: "宠物友好,带宠房", ApplicableScene: "room_selection", ReplyEnabled: true},
		}},
		{Name: "入住习惯", SemanticKey: "category.stay_habit", SortNo: 2, Children: []unifiedHotelTagSeed{
			{Name: "晚到", SemanticKey: "stay.late_arrival", Aliases: "晚入住,深夜到店", ConflictGroup: "stay.arrival", ApplicableScene: "arrival_service", ReplyEnabled: true},
			{Name: "早到", SemanticKey: "stay.early_arrival", Aliases: "早入住,提前到店", ConflictGroup: "stay.arrival", ApplicableScene: "arrival_service", ReplyEnabled: true},
			{Name: "常住", SemanticKey: "stay.frequent", Aliases: "经常住,常客", ApplicableScene: "customer_profile"},
			{Name: "连住", SemanticKey: "stay.extended", Aliases: "连续入住,多晚入住", ApplicableScene: "stay_service", ReplyEnabled: true},
			{Name: "晚退房", SemanticKey: "stay.late_checkout", Aliases: "延迟退房,晚点退房", ConflictGroup: "stay.checkout", ApplicableScene: "checkout_service", ReplyEnabled: true},
			{Name: "早退房", SemanticKey: "stay.early_checkout", Aliases: "提前退房,一早退房", ConflictGroup: "stay.checkout", ApplicableScene: "checkout_service", ReplyEnabled: true},
			{Name: "要发票", SemanticKey: "stay.invoice", Aliases: "开发票,需要发票", ApplicableScene: "invoice_service", ReplyEnabled: true},
		}},
		{Name: "出行属性", SemanticKey: "category.travel_attribute", SortNo: 3, Children: []unifiedHotelTagSeed{
			{Name: "商务", SemanticKey: "travel.business", Aliases: "出差,商务出行", ApplicableScene: "customer_profile"},
			{Name: "亲子", SemanticKey: "travel.family", Aliases: "带孩子,家庭出行", ApplicableScene: "customer_profile"},
			{Name: "情侣", SemanticKey: "travel.couple", Aliases: "情侣出行,两人约会", ApplicableScene: "customer_profile"},
			{Name: "自驾", SemanticKey: "travel.driving", Aliases: "开车,需要停车", ApplicableScene: "parking_service", ReplyEnabled: true},
			{Name: "带宠", SemanticKey: "travel.pet", Aliases: "带宠物,宠物同行", ApplicableScene: "pet_service", ReplyEnabled: true},
			{Name: "独行", SemanticKey: "travel.solo", Aliases: "一个人,单独出行", ConflictGroup: "travel.party", ApplicableScene: "customer_profile"},
			{Name: "团体", SemanticKey: "travel.group", Aliases: "多人同行,团队出行", ConflictGroup: "travel.party", ApplicableScene: "customer_profile"},
		}},
		{Name: "服务偏好", SemanticKey: "category.service_preference", SortNo: 4, Children: []unifiedHotelTagSeed{
			{Name: "硬枕", SemanticKey: "service.hard_pillow", Aliases: "硬一点的枕头,硬枕头", ConflictGroup: "service.pillow", ApplicableScene: "room_service", ReplyEnabled: true},
			{Name: "软枕", SemanticKey: "service.soft_pillow", Aliases: "软一点的枕头,软枕头", ConflictGroup: "service.pillow", ApplicableScene: "room_service", ReplyEnabled: true},
			{Name: "多要水", SemanticKey: "service.extra_water", Aliases: "多放水,多送水", ApplicableScene: "room_service", ReplyEnabled: true},
			{Name: "少打扰", SemanticKey: "service.do_not_disturb", Aliases: "不要打扰,减少打扰", ApplicableScene: "room_service", ReplyEnabled: true},
			{Name: "要清洁", SemanticKey: "service.daily_cleaning", Aliases: "每天打扫,需要清洁", ApplicableScene: "room_service", ReplyEnabled: true},
		}},
	}
}
