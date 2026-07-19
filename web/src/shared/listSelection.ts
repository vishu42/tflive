type Identified = {
  id: string;
};

export function nextSelectedID<T extends Identified>(items: T[], selectedID: string): string {
  if (selectedID && items.some((item) => item.id === selectedID)) {
    return selectedID;
  }
  return items[0]?.id ?? "";
}

export function findSelectedID<T extends Identified>(items: T[], selectedID: string): T | null {
  return items.find((item) => item.id === selectedID) ?? null;
}
