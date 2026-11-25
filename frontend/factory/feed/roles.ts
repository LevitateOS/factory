interface FeedRole {
  label: string;
  aliases: readonly string[];
  portrait?: string;
}

const roles: readonly FeedRole[] = [
  {label: 'Issue Maker', aliases: ['issue_maker']},
  {label: 'Product Lead', aliases: ['product_lead'], portrait: 'product-lead'},
  {label: 'Architect', aliases: ['architect'], portrait: 'architect'},
  {label: 'Builder', aliases: ['builder'], portrait: 'builder'},
  {label: 'Inspector', aliases: ['inspector'], portrait: 'inspector'},
  {label: 'Fixer', aliases: ['fixer'], portrait: 'fixer'},
  {label: 'Strategist', aliases: ['strategist'], portrait: 'strategist'},
  {label: 'Test Pilot', aliases: ['test_pilot'], portrait: 'test-pilot'},
  {label: 'JEV', aliases: ['jev', 'systemone', 'system_one'], portrait: 'jev'},
  {label: 'Factory', aliases: ['factory', 'system'], portrait: 'factory'},
];
const roleLabels = new Map(roles.flatMap((role) => role.aliases.map((key) => [key, role.label] as const)));

export const feedRolePortraits: Readonly<Record<string, string>> = Object.fromEntries(
  roles.flatMap((role) => (role.portrait ? [[role.label, `role-portraits/ps1-v1/${role.portrait}.png`]] : []))
);

// roleInitials is the avatar text for a known role label without a portrait,
// such as Issue Maker → IM. Other labels (people, "Post") return undefined.
export function roleInitials(label: string | undefined): string | undefined {
  if (label === undefined || !roles.some((role) => role.label === label)) return;
  return label
    .split(/\s+/)
    .map((word) => word.charAt(0))
    .join('')
    .toUpperCase();
}

export function roleKey(actor: string | undefined): string {
  return (actor ?? '').trim().toLowerCase().replace(/[\s-]+/g, '_');
}
export function roleLabel(actor: string | undefined): string | undefined {
  if (actor === undefined) return;
  return roleLabels.get(roleKey(actor)) ?? actor;
}
