<?php
use think\facade\Route;
Route::get("public/config", "Catalog/config");
Route::get("public/categories", "Catalog/categories");
Route::get("public/search", "Catalog/search");
Route::get("public/resources/:id", "Catalog/detail")->pattern(["id" => "\d+"]);
Route::get("v1/search", "Catalog/external");
Route::miss(fn() => json(["code" => 404, "message" => "接口不存在"], 404));
