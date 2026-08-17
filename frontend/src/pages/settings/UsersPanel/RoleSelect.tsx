import type { Role } from "../../../types/api";

// RoleSelect is the one control every user/group-mapping edit needs now --
// picking one of the tenant's Roles (see Settings -> Roles) instead of the
// old inline role/resourceAccess/allowedTags trio.
export function RoleSelect({
  id,
  roles,
  value,
  onChange,
}: {
  id: string;
  roles: Role[];
  value: string;
  onChange: (roleId: string) => void;
}) {
  return (
    <select id={id} className="select" value={value} onChange={(e) => onChange(e.target.value)} required>
      <option value="" disabled>
        {roles.length ? "—" : ""}
      </option>
      {roles.map((r) => (
        <option key={r.id} value={r.id}>
          {r.name}
        </option>
      ))}
    </select>
  );
}
