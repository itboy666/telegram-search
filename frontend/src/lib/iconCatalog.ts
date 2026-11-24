export interface TelegramIcon {
  label: string;
  emoji: string;
  id: string;
  position?: number;
  keywords?: string;
}

export type IconCatalog = Record<string, TelegramIcon>;
