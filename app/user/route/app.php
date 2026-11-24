<?php
use think\facade\Route;
use app\common\http\Response;
Route::post("api/login", "Account/login");
Route::post("api/bot-login", "Account/botLogin");
Route::get("api/commerce/summary", "Commerce/summary");
Route::get("api/commerce/slots", "Commerce/slots");
Route::get("api/commerce/records/:section", "Commerce/records")->pattern([
    "section" => "ledger|recharges|ads",
]);
Route::post("api/commerce/recharges", "Commerce/recharge");
Route::post("api/commerce/recharges/:id/proof", "Commerce/proof")->pattern([
    "id" => "\d+",
]);
Route::post("api/commerce/recharges/:id/check", "Commerce/check")->pattern([
    "id" => "\d+",
]);
Route::post("api/commerce/ads", "Commerce/buy");
Route::post("api/commerce/search", "Commerce/search");
Route::post("api/register", "Account/register");
Route::post("api/logout", "Account/logout");
Route::get("api/me", "Account/me");
Route::get("api/favorites", "Account/favorites");
Route::get("api/favorite-ids", "Account/favoriteIds");
Route::post("api/favorites/:id", "Account/favorite")->pattern(["id" => "\d+"]);
Route::get("api/submissions", "Account/submissions");
Route::post("api/submissions", "Account/submit");
Route::miss(
    fn() => str_starts_with(request()->pathinfo(), "api/")
        ? json(["message" => "接口不存在"], 404)
        : Response::spa(),
    "get",
);
