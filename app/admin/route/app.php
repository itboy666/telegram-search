<?php
use think\facade\Route;
use app\common\http\Response;
Route::post("api/login", "Panel/login");
Route::post("api/logout", "Panel/logout");
Route::get("api/me", "Panel/me");
Route::get("api/commerce/settings", "Commerce/settings");
Route::post("api/commerce/settings", "Commerce/saveSettings");
Route::post("api/commerce/connection", "Commerce/checkConnection");
Route::get("api/commerce/records/:section", "Commerce/listing")->pattern([
    "section" => "wallets|wallet_entries|recharges|ad_slots|ad_orders",
]);
Route::post("api/commerce/slots", "Commerce/saveSlot");
Route::post("api/commerce/slots/:id", "Commerce/saveSlot")->pattern([
    "id" => "\d+",
]);
Route::delete("api/commerce/slots/:id", "Commerce/deleteSlot")->pattern([
    "id" => "\d+",
]);
Route::post(
    "api/commerce/recharges/:id/review",
    "Commerce/reviewRecharge",
)->pattern(["id" => "\d+"]);
Route::post(
    "api/commerce/recharges/:id/check",
    "Commerce/checkRecharge",
)->pattern(["id" => "\d+"]);
Route::post("api/commerce/ads/:id/review", "Commerce/reviewAd")->pattern([
    "id" => "\d+",
]);
Route::post("api/commerce/ads/:id/resolve", "Commerce/resolveAd")->pattern([
    "id" => "\d+",
]);
Route::get("api/access/catalog", "Access/catalog");
Route::get("api/admins", "Access/admins");
Route::post("api/admins", "Access/saveAdmin");
Route::post("api/admins/:id", "Access/saveAdmin")->pattern(["id" => "\d+"]);
Route::delete("api/admins/:id", "Access/deleteAdmin")->pattern(["id" => "\d+"]);
Route::get("api/roles", "Access/roles");
Route::post("api/roles", "Access/saveRole");
Route::post("api/roles/:id", "Access/saveRole")->pattern(["id" => "\d+"]);
Route::delete("api/roles/:id", "Access/deleteRole")->pattern(["id" => "\d+"]);
Route::get("api/lookups/categories", "Panel/categoryOptions");
Route::get("api/dashboard", "Panel/dashboard");
Route::get("api/resources", "Panel/resources");
Route::get("api/resources/:id", "Panel/resource")->pattern(["id" => "\d+"]);
Route::delete("api/resources/:id", "Panel/deleteResource")->pattern([
    "id" => "\d+",
]);
Route::post("api/resources", "Panel/saveResource");
Route::post("api/resources/:id", "Panel/saveResource")->pattern([
    "id" => "\d+",
]);
Route::post("api/resources/:id/archive", "Panel/archiveResource")->pattern([
    "id" => "\d+",
]);
Route::get("api/categories", "Panel/categories");
Route::post("api/categories", "Panel/saveCategory");
Route::post("api/categories/:id", "Panel/saveCategory")->pattern([
    "id" => "\d+",
]);
Route::delete("api/categories/:id", "Panel/deleteCategory")->pattern([
    "id" => "\d+",
]);
Route::get("api/submissions", "Panel/submissions");
Route::post("api/submissions", "Panel/saveSubmission");
Route::post("api/submissions/:id", "Panel/saveSubmission")->pattern([
    "id" => "\d+",
]);
Route::delete("api/submissions/:id", "Panel/deleteSubmission")->pattern([
    "id" => "\d+",
]);
Route::post("api/submissions/:id/review", "Panel/review")->pattern([
    "id" => "\d+",
]);
Route::get("api/users", "Panel/users");
Route::post("api/users", "Panel/saveUser");
Route::post("api/users/:id", "Panel/saveUser")->pattern(["id" => "\d+"]);
Route::delete("api/users/:id", "Panel/deleteUser")->pattern(["id" => "\d+"]);
Route::post("api/users/:id/status", "Panel/userStatus")->pattern([
    "id" => "\d+",
]);
Route::post(
    "api/users/:id/submission-policy",
    "Panel/userSubmissionPolicy",
)->pattern(["id" => "\d+"]);
Route::get("api/settings", "Panel/settings");
Route::post("api/settings", "Panel/saveSettings");
Route::get("api/bot", "Panel/bot");
Route::post("api/bot", "Panel/saveBot");
Route::get("api/bot-menu", "Panel/botMenu");
Route::post("api/bot-menu", "Panel/saveBotMenu");
Route::get("api/bot-security", "Panel/botSecurity");
Route::post("api/bot-security", "Panel/saveBotSecurity");
Route::get("api/bot-groups", "Panel/botGroups");
Route::post("api/bot-groups/:id", "Panel/saveBotGroup")->pattern([
    "id" => "\d+",
]);
Route::get("api/logs", "Panel/logs");
Route::get("api/keys", "Panel/keys");
Route::post("api/keys", "Panel/createKey");
Route::post("api/keys/:id", "Panel/updateKey")->pattern(["id" => "\d+"]);
Route::delete("api/keys/:id", "Panel/deleteKey")->pattern(["id" => "\d+"]);
Route::post("api/keys/:id/revoke", "Panel/revokeKey")->pattern(["id" => "\d+"]);
Route::miss(
    fn() => str_starts_with(request()->pathinfo(), "api/")
        ? json(["message" => "接口不存在"], 404)
        : Response::spa(),
    "get",
);
