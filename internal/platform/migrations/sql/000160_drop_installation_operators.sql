-- Kurulum operatörü listesi kaldırıldı.
--
-- 000159 bu listeyi, yedekleme yetkisini firma rolünden ayırmak için eklemişti.
-- Ürün kararı, yetkinin tek başına yeterli olması yönünde: `system.backup.manage`
-- iznine sahip olan tam sistem yedeği alabilir ve geri yükleyebilir.
--
-- 000159 geri alınmıyor, üzerine yazılıyor: uygulanmış bir migration'ın içeriği
-- değiştirilmez, çünkü onu uygulamış kurulumlar o değişikliği hiç görmez.
DROP TABLE IF EXISTS installation_operators;
