package migration

import (
	"fmt"
	"testing"

	"agent-desk/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestSeedStandardHotelCustomerTagsIsIdempotent(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", t.Name())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&models.Tag{}); err != nil {
		t.Fatal(err)
	}

	if err := seedStandardHotelCustomerTags(db); err != nil {
		t.Fatal(err)
	}
	if err := seedStandardHotelCustomerTags(db); err != nil {
		t.Fatal(err)
	}

	var tags []models.Tag
	if err := db.Order("parent_id ASC, sort_no ASC, id ASC").Find(&tags).Error; err != nil {
		t.Fatal(err)
	}
	if len(tags) != 42 {
		t.Fatalf("expected 5 categories and 37 child tags, got %d", len(tags))
	}

	parentCount := 0
	childCount := 0
	seen := make(map[string]struct{}, len(tags))
	for _, tag := range tags {
		if count := len([]rune(tag.Name)); count < 1 || count > 5 {
			t.Fatalf("tag name must contain 1-5 characters: %#v", tag)
		}
		key := fmt.Sprintf("%d:%d:%s", tag.CompanyID, tag.ParentID, tag.Name)
		if _, exists := seen[key]; exists {
			t.Fatalf("duplicate seeded tag: %s", key)
		}
		seen[key] = struct{}{}
		if tag.CompanyID != 0 || tag.ReplyEnabled {
			t.Fatalf("seeded tags must be global and excluded from replies: %#v", tag)
		}
		if tag.ParentID == 0 {
			parentCount++
			if tag.AIEnabled {
				t.Fatalf("category must not be selectable by AI: %#v", tag)
			}
			continue
		}
		childCount++
		if !tag.AIEnabled || tag.SemanticKey == "" || tag.ApplicableScene == "" {
			t.Fatalf("child tag is missing AI metadata: %#v", tag)
		}
	}
	if parentCount != 5 || childCount != 37 {
		t.Fatalf("unexpected category/tag counts: parents=%d children=%d", parentCount, childCount)
	}
}
