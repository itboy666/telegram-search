<?php
namespace app\common\telegram;
interface Transport
{
    public function call(string $method, array $params = []): mixed;
}
