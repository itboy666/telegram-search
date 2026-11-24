<?php
namespace app\common\exception;
class DomainError extends \RuntimeException
{
    public function __construct(
        string $message,
        public int $status = 422,
        public array $fields = [],
    ) {
        parent::__construct($message);
    }
}
