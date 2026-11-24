<?php
namespace app\common\service;
use think\facade\Db;
use app\common\exception\DomainError;
class ResourceService
{
    public const STATUSES = [
        "published" => "已发布",
        "hidden" => "已下架",
        "archived" => "已归档",
        "blocked" => "已封禁",
        "invalid" => "已失效",
    ];
    public const TYPES = [
        "channel" => "频道",
        "group" => "群组",
        "bot" => "机器人",
        "video" => "视频",
        "article" => "图文",
        "audio" => "音乐",
        "file" => "文件",
        "shop" => "店铺",
        "other" => "其他",
        "website" => "网站",
    ];
    public static function validate(array $input): array
    {
        $title = trim((string) ($input["title"] ?? ""));
        $type = (string) ($input["type"] ?? "channel");
        $url = ResourceLink::normalize((string) ($input["url"] ?? ""), $type);
        if ($title === "" || mb_strlen($title) > 180) {
            throw new DomainError("请输入 1–180 字的资源名称。", 422, [
                "title" => "请输入有效的资源名称",
            ]);
        }
        if (!isset(self::TYPES[$type])) {
            throw new DomainError("请选择有效的资源类型。");
        }
        $category = (int) ($input["category_id"] ?? 0);
        if (
            $category &&
            !Db::name("categories")->where("id", $category)->find()
        ) {
            throw new DomainError("分类不存在，请重新选择。");
        }
        $status = (string) ($input["status"] ?? "published");
        if (!isset(self::STATUSES[$status])) {
            throw new DomainError("无效的资源状态。");
        }
        $reason = trim((string) ($input["status_reason"] ?? ""));
        if (mb_strlen($reason) > 500) {
            throw new DomainError("状态说明最多 500 字。", 422, [
                "status_reason" => "请将说明控制在 500 字以内",
            ]);
        }
        $description = trim((string) ($input["description"] ?? ""));
        if (mb_strlen($description) > 5000) {
            throw new DomainError("资源简介不能超过 5000 字。");
        }
        return [
            "title" => $title,
            "description" => $description,
            "url" => $url,
            "url_hash" => hash("sha256", $url),
            "type" => $type,
            "category_id" => $category ?: null,
            "status" => $status,
            "status_reason" => $reason,
            "tags" => mb_substr(trim((string) ($input["tags"] ?? "")), 0, 255),
            "members" =>
                isset($input["members"]) && $input["members"] !== ""
                    ? max(0, (int) $input["members"])
                    : null,
            "featured" => empty($input["featured"]) ? 0 : 1,
            "updated_at" => date("Y-m-d H:i:s"),
        ];
    }
    public static function save(
        array $input,
        ?int $id = null,
        ?int $submissionId = null,
    ): array {
        $data = self::validate($input);
        return CatalogTransaction::run(function () use (
            $input,
            $data,
            $id,
            $submissionId,
        ) {
            TelegramLink::lock([$data["url_hash"]]);
            if ($data["category_id"]) {
                AdminRecords::record("categories", $data["category_id"], true);
            }
            if (
                $id &&
                !Db::name("resources")->where("id", $id)->lock(true)->find()
            ) {
                throw new DomainError("资源不存在。", 404);
            }
            $duplicate = Db::name("resources")
                ->where("url_hash", $data["url_hash"])
                ->when($id, fn($q) => $q->where("id", "<>", $id))
                ->lock(true)
                ->find();
            if ($duplicate) {
                $message = TelegramLink::resourceMessage($duplicate);
                throw new DomainError($message, 422, ["url" => $message]);
            }
            $submission = Db::name("submissions")
                ->where("url_hash", $data["url_hash"])
                ->lock(true)
                ->find();
            if (
                $submission &&
                !(
                    $submissionId &&
                    (int) $submission["id"] === $submissionId &&
                    $submission["status"] === "pending"
                ) &&
                !($id && (int) ($submission["resource_id"] ?? 0) === $id)
            ) {
                $message = TelegramLink::submissionMessage($submission);
                throw new DomainError($message, 422, ["url" => $message]);
            }
            if ($id) {
                Db::name("resources")->where("id", $id)->update($data);
            } else {
                $id = Db::name("resources")->insertGetId(
                    $data + [
                        "created_at" => date("Y-m-d H:i:s"),
                        "source" => (string) ($input["source"] ?? "admin"),
                        "owner_id" => $input["owner_id"] ?? null,
                    ],
                );
            }
            return Db::name("resources")->where("id", $id)->find();
        });
    }
}
