<?php
namespace app\common\service;
use think\facade\Db;
use app\common\exception\DomainError;
class SearchService
{
    public function search(
        array $input,
        string $source = "web",
        bool $admin = false,
        bool $record = true,
        ?array $visibleFields = null,
    ): array {
        $start = microtime(true);
        $keyword = trim((string) ($input["q"] ?? ""));
        if (mb_strlen($keyword) > 120) {
            throw new DomainError("搜索关键词最多 120 字。");
        }
        $canRead = fn(string $field) => $visibleFields === null ||
            in_array($field, $visibleFields, true);
        foreach (["status", "type", "category_id", "featured"] as $field) {
            if ($admin && !empty($input[$field]) && !$canRead($field)) {
                throw new DomainError("无权按此字段筛选。", 403);
            }
        }
        if ($admin && !empty($input["days"]) && !$canRead("created_at")) {
            throw new DomainError("无权按创建时间筛选。", 403);
        }
        if (
            $admin &&
            ($input["sort"] ?? "") === "members" &&
            !$canRead("members")
        ) {
            throw new DomainError("无权按成员数量排序。", 403);
        }
        $page = max(
            1,
            min($admin ? 10000000 : 100, (int) ($input["page"] ?? 1)),
        );
        $size = max(1, min(50, (int) ($input["page_size"] ?? 12)));
        $query = Db::name("resources")
            ->alias("r")
            ->leftJoin(["ts_categories" => "c"], "r.category_id=c.id");
        if ($admin && isset($input["id"]) && $input["id"] !== "") {
            $id = filter_var($input["id"], FILTER_VALIDATE_INT, [
                "options" => ["min_range" => 1],
            ]);
            if (!$id) {
                throw new DomainError("资源 ID 必须为正整数。");
            }
            $query->where("r.id", $id);
        }
        if (!$admin) {
            $query->where("r.status", "published");
        } elseif (!empty($input["status"])) {
            $query->where("r.status", (string) $input["status"]);
        }
        if (!empty($input["type"])) {
            $query->where("r.type", (string) $input["type"]);
        }
        if (!empty($input["category_id"])) {
            $query->where("r.category_id", (int) $input["category_id"]);
        }
        if (!empty($input["featured"])) {
            $query->where("r.featured", 1);
        }
        if (in_array((int) ($input["days"] ?? 0), [3, 7, 30], true)) {
            $query->where(
                "r.created_at",
                ">=",
                date("Y-m-d H:i:s", time() - (int) $input["days"] * 86400),
            );
        }
        if ($keyword !== "") {
            $pattern = "%" . addcslashes($keyword, "\\%_") . "%";
            $searchFields = array_values(
                array_filter(
                    $admin
                        ? ["title", "description", "tags", "url"]
                        : ["title", "description", "tags"],
                    $canRead,
                ),
            );
            if (!$searchFields) {
                throw new DomainError("没有可搜索字段的查看权限。", 403);
            }
            $query->where(function ($q) use ($searchFields, $pattern) {
                foreach ($searchFields as $index => $field) {
                    if ($index === 0) {
                        $q->whereLike("r." . $field, $pattern);
                    } else {
                        $q->whereOr("r." . $field, "like", $pattern);
                    }
                }
            });
        }
        $total = (clone $query)->count();
        $sort =
            ($input["sort"] ?? "newest") === "members"
                ? "r.members desc,r.id desc"
                : ($canRead("featured")
                    ? "r.featured desc,r.id desc"
                    : "r.id desc");
        $items = $query
            ->field("r.*,c.name as category_name")
            ->order($sort)
            ->page($page, $size)
            ->select()
            ->toArray();
        foreach ($items as &$item) {
            unset($item["url_hash"]);
            $item["type_label"] =
                ResourceService::TYPES[$item["type"]] ?? "其他";
            if (!$admin) {
                $item["source_attribution"] = CatalogSource::attribution(
                    (string) $item["source"],
                );
            }
        }
        unset($item);
        $duration = (int) round((microtime(true) - $start) * 1000);
        if ($record && !$admin && $keyword !== "" && $page === 1) {
            Db::name("search_logs")->insert([
                "query" => $keyword,
                "source" => $source,
                "result_count" => $total,
                "duration_ms" => $duration,
                "created_at" => date("Y-m-d H:i:s"),
            ]);
        }
        return [
            "items" => $items,
            "total" => $total,
            "page" => $page,
            "page_size" => $size,
            "total_pages" => (int) ceil($total / $size),
            "has_more" => $page * $size < $total,
            "duration_ms" => $duration,
        ];
    }
}
