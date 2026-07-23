package services

// Store customer tag decisions are created only inside
// CustomerTagService.ReconcileStoreRelationTags. This file intentionally
// prevents the CRUD generator from exposing update or delete operations for
// append-only decision evidence.
