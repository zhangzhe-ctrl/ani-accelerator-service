UPDATE accelerator.profiles SET payload=payload #- $2::text[] WHERE profile_id=$1;
