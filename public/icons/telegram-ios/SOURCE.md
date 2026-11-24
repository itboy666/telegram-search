# Telegram iOS Icons

These icons come from the Telegram custom emoji pack `tgiosicons`, titled **Telegram iOS Icons**:

https://t.me/addemoji/tgiosicons

The mapping in `config/tgios_icons.php` uses IDs verified against Telegram's `getStickerSet`. IDs are strings, not JavaScript numbers.

The full-pack snapshot synchronized on 2026-11-16 contains **399 unique original icons**. The catalog has 400 compatible keys because the existing `menu` and `other` names reference the same original ID. Existing keys are preserved; newly added icons use stable `ios_<custom_emoji_id>` keys. Pickers deduplicate aliases by original ID while retaining the currently selected legacy key.

The website uses SVGs exported from frame 0 of the original TGS files by python-lottie 0.7.2. Original artwork is not redrawn. CSS masks apply the current text color; the monochrome pack declares `needs_repainting`. Original TGS sources are retained in `resources/icons/telegram-ios/`.

`resources/icons/telegram-ios/manifest.json` records the sync timestamp, original IDs, emoji annotations, and SHA-256 of every retained TGS source. Chinese labels and bilingual search families in `resources/icons/telegram-ios-labels.php` are derived from those emoji annotations, not individual titles supplied by the artist. Variants use their position in this snapshot as a visible distinguishing number; persisted menu settings use stable keys, not positions. The pack includes both outline and filled variants, all preserved as supplied.

Telegram messages and keyboards use the same original custom emoji IDs. Static website assets are included, so running the application does not require Python or a Telegram file download.

To refresh the complete pack, run `php think bot:icons --all` with the converter described in the README. Valid downloaded sources and SVGs are reused, and the catalog is published only after all exports succeed.

This attribution does not assert a redistribution license for the third-party artwork. The application’s code license does not replace the artwork owner’s rights.
