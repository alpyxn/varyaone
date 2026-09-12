-- TOTP kod tekrarını engellemek için son kabul edilen zaman adımını sakla.
--
-- VerifyTOTP kabul penceresi içindeki (önceki/şimdiki/sonraki 30 saniyelik
-- adım) bir kodu doğruluyordu ama hangi adımın kullanıldığını hiçbir yerde
-- tutmuyordu; aynı kod pencere boyunca tekrar tekrar kabul edilebiliyordu.
ALTER TABLE users ADD COLUMN totp_last_step bigint;
