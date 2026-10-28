SELECT has_table_privilege(current_user,'accelerator.gpu_metric_history','UPDATE,DELETE,TRUNCATE'),
       has_table_privilege(current_user,'accelerator.gpu_metric_index','INSERT,UPDATE,DELETE,TRUNCATE');
