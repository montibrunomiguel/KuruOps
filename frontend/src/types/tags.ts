// Mirrors backend/internal/domain/tag.go -- the Settings-managed catalog of
// tag names. alerts/incidents only ever carry tag names (string[]), never a
// reference to this row, so this type is only used by the catalog UI itself
// and by TagPicker's source list.
export interface Tag {
  id: string;
  tenantId: string;
  name: string;
  color?: string;
  createdBy?: string;
  createdAt: string;
}
