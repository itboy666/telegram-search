<?php
namespace app\common\service;
use think\facade\Db;
use app\common\exception\DomainError;

class SubmissionService
{
    public static function create(int $userId, array $input): array
    {
        self::assertAllowed($userId);
        RateLimit::hit("submission:" . $userId, 10, 3600);
        return self::persist($userId, $input);
    }

    public static function createFromTelegram(
        int $userId,
        string $link,
        ?TelegramMetadata $metadata = null,
    ): array {
        self::assertAllowed($userId);
        RateLimit::hit("submission:" . $userId, 10, 3600);
        $url = TelegramLink::normalize($link);
        self::assertAvailable(hash("sha256", $url));
        // Do network work before acquiring catalog locks; recheck under the lock below.
        $data = ($metadata ?? new TelegramMetadata())->fetch($url);
        $result = self::persist($userId, ["url" => $url] + $data);
        return $result + ["title" => $data["title"]];
    }

    private static function persist(int $userId, array $input): array
    {
        $url = ResourceLink::normalize(
            (string) ($input["url"] ?? ""),
            (string) ($input["type"] ?? "channel"),
        );
        $hash = hash("sha256", $url);
        return CatalogTransaction::run(function () use (
            $userId,
            $input,
            $url,
            $hash,
        ) {
            TelegramLink::lock([$hash]);
            self::assertAllowed($userId, true);
            self::assertAvailable($hash, true);
            $data = ResourceService::validate(
                array_replace($input, ["url" => $url]),
            );
            if ($data["category_id"]) {
                AdminRecords::record("categories", $data["category_id"], true);
            }
            $id = Db::name("submissions")->insertGetId(
                array_intersect_key(
                    $data,
                    array_flip([
                        "title",
                        "url",
                        "url_hash",
                        "type",
                        "description",
                        "category_id",
                    ]),
                ) + ["user_id" => $userId, "created_at" => date("Y-m-d H:i:s")],
            );
            return ["id" => $id, "status" => "pending"];
        });
    }
    private static function assertAllowed(int $userId, bool $lock = false): void
    {
        self::assertUser(
            Db::name("users")->where("id", $userId)->lock($lock)->find(),
        );
        if (Settings::get("submissions_enabled") !== "1") {
            throw new DomainError("暂未开放资源提交，请稍后再来。", 403);
        }
    }
    private static function assertAvailable(
        string $hash,
        bool $lock = false,
    ): void {
        $resource = Db::name("resources")
            ->where("url_hash", $hash)
            ->lock($lock)
            ->find();
        if ($resource) {
            $message = TelegramLink::resourceMessage($resource);
            throw new DomainError($message, 422, ["url" => $message]);
        }
        $submission = Db::name("submissions")
            ->where("url_hash", $hash)
            ->lock($lock)
            ->find();
        if ($submission) {
            $message = TelegramLink::submissionMessage($submission);
            throw new DomainError($message, 422, ["url" => $message]);
        }
    }
    private static function assertUser(?array $user): void
    {
        if (!$user || !$user["status"]) {
            throw new DomainError("当前账号已停用，不能提交收录。", 403);
        }
        if (!(int) $user["submission_allowed"]) {
            $reason = trim((string) $user["submission_note"]);
            throw new DomainError(
                "你已被禁止提交收录。" .
                    ($reason !== "" ? "\n原因：" . $reason : ""),
                403,
            );
        }
    }
}
