import { defineComponent, h } from "vue";
import IosIcon from "../components/IosIcon.vue";
// App-authored icons use the same Telegram iOS Icons source as the bot.
const icon = (name: string) =>
  defineComponent({
    name: `Ios${name}`,
    setup: () => () => h(IosIcon, { name }),
  });
export const Search = icon("search");
export const Collection = icon("other");
export const Connection = icon("link");
export const ArrowRight = icon("right");
export const ArrowLeft = icon("left");
export const TopRight = icon("right");
export const Star = icon("star");
export const Promotion = icon("telegram");
export const Plus = icon("plus");
export const Refresh = icon("refresh");
export const Edit = icon("edit");
export const FolderOpened = icon("folder");
export const DocumentChecked = icon("check");
export const User = icon("user");
export const Clock = icon("clock");
export const Key = icon("lock");
export const Setting = icon("settings");
export const Link = icon("link");
export const Check = icon("check");
export const Odometer = icon("chart");
export const Expand = icon("menu");
export const Close = icon("close");
export const SwitchButton = icon("logout");
