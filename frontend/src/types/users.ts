// Mirrors backend/internal/domain.UserSummary -- the minimal {id, name}
// projection returned by GET /api/v1/users/directory, which any
// authenticated user can call (unlike the full admin user list under
// Settings -> Users & Roles) to populate an owner picker or resolve a
// name for a userID they can see but don't have admin access to look up.
export interface UserSummary {
  id: string;
  name: string;
}
