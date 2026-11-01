export function isAnonymousLayout(value: unknown): value is 'catalog' | 'identity' { return value === 'catalog' || value === 'identity' }
