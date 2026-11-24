<?php
namespace app\common\http;
use app\common\exception\DomainError;
class Response
{
    public static function run(callable $callback): \think\Response
    {
        try {
            $request = request();
            if (
                !in_array($request->method(), ["GET", "HEAD", "OPTIONS"], true)
            ) {
                $origin = (string) $request->header("origin", "");
                if (
                    $origin !== "" &&
                    strtolower($origin) !== strtolower($request->domain())
                ) {
                    throw new DomainError(
                        "不允许跨站提交，请从本站页面操作。",
                        403,
                    );
                }
            }
            return json([
                "code" => 0,
                "data" => $callback(),
                "message" => "ok",
            ])->header(["Cache-Control" => "no-store"]);
        } catch (DomainError $e) {
            return json(
                [
                    "code" => $e->status,
                    "message" => $e->getMessage(),
                    "errors" => $e->fields,
                ],
                $e->status,
            );
        } catch (\Throwable $e) {
            \think\facade\Log::error(get_class($e) . ": " . $e->getMessage());
            return json(
                ["code" => 500, "message" => "服务暂时不可用，请稍后重试。"],
                500,
            );
        }
    }
    public static function spa(): \think\Response
    {
        $file = root_path() . "public/build/index.html";
        return is_file($file)
            ? response(file_get_contents($file))->contentType("text/html")
            : response("请先执行 frontend 中的 pnpm build。", 503);
    }
}
