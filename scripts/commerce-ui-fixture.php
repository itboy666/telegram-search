<?php
/** Disposable UI account, with no funds. Never run on a public production deployment. */
require dirname(__DIR__) . "/vendor/autoload.php";
new think\App(dirname(__DIR__))->initialize();
use think\facade\Db;
use app\common\service\{BotLogin, CatalogTransaction};
$file = runtime_path() . "commerce-ui.json";
if (($argv[1] ?? "") === "cleanup") {
    if (!is_file($file)) {
        exit(0);
    }
    $state = json_decode(file_get_contents($file), true);
    CatalogTransaction::run(function () use ($state) {
        $u = Db::name("users")
            ->where("id", $state["id"])
            ->where("username", $state["username"])
            ->lock(true)
            ->find();
        if (!$u || !str_starts_with($u["username"], "commerce_ui_")) {
            throw new RuntimeException("Fixture identity mismatch");
        }
        if (Db::name("wallet_entries")->where("user_id", $u["id"])->count()) {
            throw new RuntimeException("Fixture has money; do not delete");
        }
        foreach (
            ["sessions", "bot_login_codes", "recharges", "wallets"]
            as $table
        ) {
            Db::name($table)->where("user_id", $u["id"])->delete();
        }
        Db::name("users")->where("id", $u["id"])->delete();
    });
    unlink($file);
    echo "Disposable UI account and unpaid test orders removed.\n";
    exit(0);
}
if (is_file($file)) {
    throw new RuntimeException("Clean the existing fixture first.");
}
$name = "commerce_ui_" . bin2hex(random_bytes(5));
$id = Db::name("users")->insertGetId([
    "username" => $name,
    "display_name" => "界面验收",
    "created_at" => date("Y-m-d H:i:s"),
]);
$code = BotLogin::issue((int) $id);
$mask = umask(0077);
try {
    file_put_contents(
        $file,
        json_encode(["id" => $id, "username" => $name, "code" => $code]),
    );
} finally {
    umask($mask);
}
echo "Disposable UI account ready; credentials kept in private runtime file.\n";
