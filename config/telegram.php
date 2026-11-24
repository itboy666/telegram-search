<?php
return ['token' => env('telegram.token', ''), 'poll_timeout' => (int) env('telegram.poll_timeout', 20)];
