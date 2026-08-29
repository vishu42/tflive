type Identified = {
  id: string;
};

export function findSelectedID<T extends Identified>(items: T[], selectedID: string): T | null {
  return items.find((item) => item.id === selectedID) ?? null;
}
