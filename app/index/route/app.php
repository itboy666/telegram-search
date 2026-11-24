<?php
use think\facade\Route;
Route::miss(fn() => \app\common\http\Response::spa(), "get");
